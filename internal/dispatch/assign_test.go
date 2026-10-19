package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
	"github.com/Kaylan00/lastmile-go/internal/platform/postgres/pgtest"
	"github.com/Kaylan00/lastmile-go/migrations"
)

// O teste mais importante do projeto: 100 goroutines tentam alocar ao mesmo
// tempo, mas só existem 10 motoristas. Tem que sair exatamente 10 alocações,
// nenhum motorista com dois pedidos e nenhum pedido com dois motoristas.
// Rode com -race: go test -race ./internal/dispatch/
func TestAssignNext_100GoroutinesCompeteFor10Drivers(t *testing.T) {
	const goroutines, drivers = 100, 10

	// 50 conexões no pool: muitas transações abertas ao mesmo tempo, disputando as mesmas linhas.
	pool := pgtest.New(t, migrations.Dispatch(), 50)
	store := NewStore(pool)
	ctx := context.Background()

	for i := range drivers {
		pos := geo.Point{Lat: se.Lat + float64(i)*0.001, Lng: se.Lng}
		if _, err := store.SetAvailability(ctx, fmt.Sprintf("drv-%02d", i), true, &pos); err != nil {
			t.Fatal(err)
		}
	}
	for range goroutines {
		if _, err := store.CreateOrder(ctx, se, paulista); err != nil {
			t.Fatal(err)
		}
	}

	type result struct {
		a   Assignment
		err error
	}
	results := make(chan result, goroutines) // com buffer: nenhuma goroutine trava ao enviar
	start := make(chan struct{})             // sem buffer: serve de "largada" para todas juntas
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			<-start
			a, err := store.AssignNext(ctx)
			results <- result{a, err}
		})
	}
	close(start) // fechar o canal libera todas as goroutines que esperavam nele
	wg.Wait()
	close(results)

	assigned := 0
	byDriver := map[string]string{} // motorista -> pedido
	byOrder := map[string]string{}  // pedido -> motorista
	for r := range results {
		switch {
		case r.err == nil:
			assigned++
			if prev, dup := byDriver[r.a.DriverID]; dup {
				t.Errorf("motorista %s alocado duas vezes (%s e %s)", r.a.DriverID, prev, r.a.OrderID)
			}
			if prev, dup := byOrder[r.a.OrderID]; dup {
				t.Errorf("pedido %s alocado duas vezes (%s e %s)", r.a.OrderID, prev, r.a.DriverID)
			}
			byDriver[r.a.DriverID] = r.a.OrderID
			byOrder[r.a.OrderID] = r.a.DriverID
		case errors.Is(r.err, ErrNoDriverAvailable), errors.Is(r.err, ErrNoPendingOrder):
			// esperado: só 10 conseguem
		default:
			t.Errorf("erro inesperado: %v", r.err)
		}
	}
	if assigned != drivers {
		t.Fatalf("alocações = %d, quero exatamente %d", assigned, drivers)
	}

	// Confere o estado final no próprio banco.
	var assignedOrders, distinctDrivers, busyDrivers, consistent int
	err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM orders WHERE status = 'assigned'),
		  (SELECT count(DISTINCT driver_id) FROM orders WHERE status = 'assigned'),
		  (SELECT count(*) FROM drivers WHERE status = 'busy'),
		  (SELECT count(*) FROM drivers d JOIN orders o ON o.id = d.current_order_id AND o.driver_id = d.id)
	`).Scan(&assignedOrders, &distinctDrivers, &busyDrivers, &consistent)
	if err != nil {
		t.Fatal(err)
	}
	if assignedOrders != drivers || distinctDrivers != drivers || busyDrivers != drivers || consistent != drivers {
		t.Fatalf("estado no banco: pedidos alocados=%d, motoristas distintos=%d, ocupados=%d, consistentes=%d; quero %d em todos",
			assignedOrders, distinctDrivers, busyDrivers, consistent, drivers)
	}
}

func TestAssignNext_PicksNearestDriver(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	drivers := map[string]geo.Point{
		"longe":   {Lat: -23.70, Lng: -46.80},
		"perto":   {Lat: -23.5510, Lng: -46.6340}, // ~100 m da Sé
		"medio":   paulista,
		"ocupado": se, // no mesmo ponto, mas vai ficar offline
	}
	for id, pos := range drivers {
		if _, err := store.SetAvailability(ctx, id, true, &pos); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SetAvailability(ctx, "ocupado", false, nil); err != nil {
		t.Fatal(err)
	}

	o, _ := store.CreateOrder(ctx, se, pinheiro)
	a, err := store.AssignNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a.OrderID != o.ID || a.DriverID != "perto" {
		t.Fatalf("alocou %+v, quero o pedido %s com o motorista 'perto'", a, o.ID)
	}
	if a.DistanceKm > 0.2 {
		t.Errorf("distância %.3f km, esperava ~0,1 km", a.DistanceKm)
	}

	got, _ := store.GetOrder(ctx, o.ID)
	if got.Status != order.StatusAssigned || got.DriverID != "perto" || got.AssignedAt == nil {
		t.Fatalf("pedido depois da alocação: %+v", got)
	}
	d, _ := store.GetDriver(ctx, "perto")
	if d.Status != DriverBusy || d.CurrentOrderID != o.ID {
		t.Fatalf("motorista depois da alocação: %+v", d)
	}
}

func TestAssignNext_OldestOrderFirst(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	first, _ := store.CreateOrder(ctx, se, paulista)
	_, _ = store.CreateOrder(ctx, se, pinheiro)
	_, _ = store.SetAvailability(ctx, "drv-01", true, &se)

	a, err := store.AssignNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a.OrderID != first.ID {
		t.Fatalf("alocou %s, quero o mais antigo %s", a.OrderID, first.ID)
	}
}

func TestAssignNext_NothingToDo(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.AssignNext(ctx); !errors.Is(err, ErrNoPendingOrder) {
		t.Fatalf("sem pedidos: err = %v, quero ErrNoPendingOrder", err)
	}

	o, _ := store.CreateOrder(ctx, se, paulista)
	if _, err := store.AssignNext(ctx); !errors.Is(err, ErrNoDriverAvailable) {
		t.Fatalf("sem motoristas: err = %v, quero ErrNoDriverAvailable", err)
	}
	// o rollback devolve o pedido para a fila
	if got, _ := store.GetOrder(ctx, o.ID); got.Status != order.StatusPending {
		t.Fatalf("pedido deveria continuar pending, está %s", got.Status)
	}
}

// Versão determinística do que o teste das 100 goroutines faz por sorteio:
// uma transação segura os locks e a outra, em vez de esperar, pega as próximas linhas.
func TestAssignNext_SkipsRowsLockedByAnotherTransaction(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()
	first, _ := store.CreateOrder(ctx, se, paulista)
	second, _ := store.CreateOrder(ctx, se, pinheiro)
	near := geo.Point{Lat: -23.5510, Lng: -46.6340}
	_, _ = store.SetAvailability(ctx, "perto", true, &near)
	_, _ = store.SetAvailability(ctx, "longe", true, &pinheiro)

	// T1: trava o pedido mais antigo e o motorista mais próximo e NÃO termina a transação.
	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if _, err := tx1.Exec(ctx, `SELECT 1 FROM orders WHERE id = $1 FOR UPDATE`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx1.Exec(ctx, `SELECT 1 FROM drivers WHERE id = 'perto' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}

	// T2: AssignNext não pode ficar esperando T1. Se esperasse, estouraria o prazo de 2 s.
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	a, err := store.AssignNext(ctx2)
	if err != nil {
		t.Fatalf("AssignNext esperou ou falhou: %v", err)
	}
	if a.OrderID != second.ID || a.DriverID != "longe" {
		t.Fatalf("alocou %+v, quero o 2º pedido com o motorista 'longe' (os outros estão travados)", a)
	}
}
