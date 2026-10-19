package dispatch

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

func outboxTypes(t *testing.T, pool *pgxpool.Pool, orderID string) []events.Type {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT event_type FROM outbox WHERE order_id = $1 ORDER BY id`, orderID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var types []events.Type
	for rows.Next() {
		var typ events.Type
		if err := rows.Scan(&typ); err != nil {
			t.Fatal(err)
		}
		types = append(types, typ)
	}
	return types
}

func assignedOrder(t *testing.T, store *Store) Order {
	t.Helper()
	ctx := context.Background()
	o, _ := store.CreateOrder(ctx, se, paulista)
	_, _ = store.SetAvailability(ctx, "drv-01", true, &pinheiro)
	if _, err := store.AssignNext(ctx); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestReportProgress_FullDelivery(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()
	o := assignedOrder(t, store)

	steps := []order.Status{order.StatusPickedUp, order.StatusOutForDelivery, order.StatusDelivered}
	for _, st := range steps {
		got, changed, err := store.ReportProgress(ctx, Progress{OrderID: o.ID, DriverID: "drv-01", Status: st, Position: &paulista})
		if err != nil || !changed || got.Status != st {
			t.Fatalf("%s: status=%s changed=%v err=%v", st, got.Status, changed, err)
		}
	}

	want := []events.Type{events.OrderAssigned, events.OrderPickedUp, events.OrderOutForDelivery, events.OrderDelivered}
	got := outboxTypes(t, pool, o.ID)
	if len(got) != len(want) {
		t.Fatalf("eventos na outbox = %v, quero %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("eventos na outbox = %v, quero %v", got, want)
		}
	}

	// depois da entrega o motorista está livre de novo, onde entregou
	d, _ := store.GetDriver(ctx, "drv-01")
	if d.Status != DriverAvailable || d.CurrentOrderID != "" || *d.Position != paulista {
		t.Fatalf("motorista depois da entrega: %+v", d)
	}
}

func TestReportProgress_Rules(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()
	o := assignedOrder(t, store)

	tests := []struct {
		name        string
		p           Progress
		wantErr     error
		wantChanged bool
	}{
		{"outro motorista", Progress{OrderID: o.ID, DriverID: "drv-99", Status: order.StatusPickedUp}, ErrWrongDriver, false},
		{"pula etapa", Progress{OrderID: o.ID, DriverID: "drv-01", Status: order.StatusDelivered}, ErrInvalidTransition, false},
		{"pedido inexistente", Progress{OrderID: "00000000-0000-0000-0000-000000000000", DriverID: "drv-01", Status: order.StatusPickedUp}, ErrNotFound, false},
		{"coletou", Progress{OrderID: o.ID, DriverID: "drv-01", Status: order.StatusPickedUp}, nil, true},
		{"coletou de novo (retry do app)", Progress{OrderID: o.ID, DriverID: "drv-01", Status: order.StatusPickedUp}, nil, false},
		{"volta para alocado", Progress{OrderID: o.ID, DriverID: "drv-01", Status: order.StatusAssigned}, ErrInvalidTransition, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, changed, err := store.ReportProgress(ctx, tt.p)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, quero %v", err, tt.wantErr)
			}
			if changed != tt.wantChanged {
				t.Fatalf("changed = %v, quero %v", changed, tt.wantChanged)
			}
		})
	}

	// o retry não gerou evento repetido: assigned + picked_up
	if got := outboxTypes(t, pool, o.ID); len(got) != 2 {
		t.Fatalf("eventos = %v, quero 2", got)
	}
}

func TestUpdateLocation(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()

	// motorista livre: grava a posição, mas não há pedido para avisar
	_, _ = store.SetAvailability(ctx, "drv-02", true, &se)
	if _, err := store.UpdateLocation(ctx, "drv-02", paulista); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&n)
	if n != 0 {
		t.Fatalf("motorista livre gerou %d eventos", n)
	}
	_, _ = store.SetAvailability(ctx, "drv-02", false, nil) // sai do turno para não pegar o pedido abaixo

	// motorista com pedido: gera order.location_updated
	o := assignedOrder(t, store)
	moved := geo.Point{Lat: -23.5600, Lng: -46.6700}
	if _, err := store.UpdateLocation(ctx, "drv-01", moved); err != nil {
		t.Fatal(err)
	}
	types := outboxTypes(t, pool, o.ID)
	if len(types) != 2 || types[1] != events.OrderLocation {
		t.Fatalf("eventos = %v, quero [order.assigned order.location_updated]", types)
	}

	if _, err := store.UpdateLocation(ctx, "nao-existe", moved); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, quero ErrNotFound", err)
	}
}
