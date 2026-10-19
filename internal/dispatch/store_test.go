package dispatch

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
	"github.com/Kaylan00/lastmile-go/internal/platform/postgres/pgtest"
	"github.com/Kaylan00/lastmile-go/migrations"
)

var (
	se       = geo.Point{Lat: -23.5505, Lng: -46.6333} // Praça da Sé
	paulista = geo.Point{Lat: -23.5614, Lng: -46.6559} // MASP
	pinheiro = geo.Point{Lat: -23.5670, Lng: -46.7020} // Pinheiros
)

func newTestStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.New(t, migrations.Dispatch(), 20)
	return NewStore(pool), pool
}

func TestCreateAndGetOrder(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	created, err := store.CreateOrder(ctx, se, paulista)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != order.StatusPending || created.DriverID != "" {
		t.Fatalf("pedido novo deveria ser pending sem motorista: %+v", created)
	}

	got, err := store.GetOrder(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pickup != se || got.Dropoff != paulista {
		t.Fatalf("coordenadas diferentes: %+v", got)
	}

	if _, err := store.GetOrder(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("id inexistente: err = %v, quero ErrNotFound", err)
	}
	if _, err := store.GetOrder(ctx, "nao-e-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("id inválido: err = %v, quero ErrNotFound", err)
	}
}

func TestSetAvailability(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()

	d, err := store.SetAvailability(ctx, "drv-01", true, &se)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != DriverAvailable || d.Position == nil || *d.Position != se {
		t.Fatalf("motorista deveria estar livre na Sé: %+v", d)
	}

	// offline sem mandar posição mantém a última posição conhecida
	d, err = store.SetAvailability(ctx, "drv-01", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != DriverOffline || d.Position == nil || *d.Position != se {
		t.Fatalf("offline deveria manter a posição: %+v", d)
	}

	// motorista ocupado não pode se marcar livre (ele só libera ao terminar a entrega)
	o, _ := store.CreateOrder(ctx, se, paulista)
	if _, err := pool.Exec(ctx, `
		UPDATE orders SET status = 'assigned', driver_id = 'drv-01' WHERE id = $1;
	`, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE drivers SET status = 'busy', current_order_id = $1 WHERE id = 'drv-01'`, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAvailability(ctx, "drv-01", true, &paulista); !errors.Is(err, ErrDriverBusy) {
		t.Fatalf("err = %v, quero ErrDriverBusy", err)
	}
}

// As constraints do banco são a última linha de defesa contra estado inválido.
func TestDatabaseConstraints(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()
	o1, _ := store.CreateOrder(ctx, se, paulista)
	o2, _ := store.CreateOrder(ctx, se, pinheiro)
	if _, err := store.SetAvailability(ctx, "drv-01", true, &se); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		sql  string
		args []any
	}{
		{"livre sem posição", `INSERT INTO drivers (id, status) VALUES ('drv-x', 'available')`, nil},
		{"ocupado sem pedido", `UPDATE drivers SET status = 'busy' WHERE id = 'drv-01'`, nil},
		{"pendente com motorista", `UPDATE orders SET driver_id = 'drv-01' WHERE id = $1`, []any{o1.ID}},
		{"status desconhecido", `UPDATE orders SET status = 'lost' WHERE id = $1`, []any{o1.ID}},
		{"dois pedidos ativos no mesmo motorista", `
			UPDATE orders SET status = 'assigned', driver_id = 'drv-01' WHERE id IN ($1, $2)`, []any{o1.ID, o2.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.sql, tt.args...); err == nil {
				t.Fatal("o banco deveria ter recusado")
			}
		})
	}
}

// A função haversine_km do banco precisa dar o mesmo resultado que geo.DistanceKm,
// porque a alocação ordena pelo SQL e o ETA calcula pelo Go.
func TestHaversineSQLMatchesGo(t *testing.T) {
	_, pool := newTestStore(t)
	pairs := [][2]geo.Point{{se, paulista}, {se, pinheiro}, {{Lat: 0, Lng: 0}, {Lat: 0, Lng: 180}}, {se, se}}
	for _, p := range pairs {
		var sqlKm float64
		err := pool.QueryRow(context.Background(), `SELECT haversine_km($1, $2, $3, $4)`,
			p[0].Lat, p[0].Lng, p[1].Lat, p[1].Lng).Scan(&sqlKm)
		if err != nil {
			t.Fatal(err)
		}
		if goKm := geo.DistanceKm(p[0], p[1]); math.Abs(goKm-sqlKm) > 1e-6 {
			t.Errorf("%v -> %v: SQL %.9f km, Go %.9f km", p[0], p[1], sqlKm, goKm)
		}
	}
}
