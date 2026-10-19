package tracking

import (
	"context"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/order"
	"github.com/Kaylan00/lastmile-go/internal/platform/postgres/pgtest"
	"github.com/Kaylan00/lastmile-go/migrations"
)

func newTestStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.New(t, migrations.Tracking(), 30)
	return NewStore(pool), pool
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestProcess_DuplicateIsIgnored(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()
	e := ev(events.OrderAssigned, 0, pt(-23.54, -46.62))

	first, err := store.Process(ctx, e, "")
	if err != nil || first.Duplicate || !first.Changed {
		t.Fatalf("primeira vez: %+v, %v", first, err)
	}
	second, err := store.Process(ctx, e, "")
	if err != nil || !second.Duplicate {
		t.Fatalf("segunda vez deveria ser duplicada: %+v, %v", second, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM processed_events`); n != 1 {
		t.Fatalf("processed_events = %d, quero 1", n)
	}
}

// A mesma mensagem entregue para 20 consumidores ao mesmo tempo (duas réplicas,
// redelivery...) é aplicada uma única vez.
func TestProcess_ConcurrentDuplicates(t *testing.T) {
	store, pool := newTestStore(t)
	e := ev(events.OrderAssigned, 0, pt(-23.54, -46.62))

	var mu sync.Mutex
	applied := 0
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			res, err := store.Process(context.Background(), e, "")
			if err != nil {
				t.Error(err)
				return
			}
			if !res.Duplicate {
				mu.Lock()
				applied++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if applied != 1 {
		t.Fatalf("o evento foi aplicado %d vezes, quero 1", applied)
	}
	if n := count(t, pool, `SELECT count(*) FROM processed_events WHERE event_id = $1`, e.ID); n != 1 {
		t.Fatalf("processed_events = %d, quero 1", n)
	}
}

// Eventos diferentes do mesmo pedido processados em paralelo e fora de ordem
// (com repetições) terminam no mesmo estado que a ordem certa.
func TestProcess_ConcurrentOutOfOrderSameFinalState(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	sequence := []events.Event{
		ev(events.OrderAssigned, 0, pt(-23.54, -46.62)),
		ev(events.OrderLocation, 2, pt(-23.545, -46.625)),
		ev(events.OrderPickedUp, 5, pt(-23.5505, -46.6333)),
		ev(events.OrderOutForDelivery, 6, nil),
		ev(events.OrderLocation, 9, pt(-23.556, -46.645)),
		ev(events.OrderDelivered, 15, pt(-23.5614, -46.6559)),
	}
	deliveries := append([]events.Event(nil), sequence...)
	deliveries = append(deliveries, sequence[2], sequence[5], sequence[0]) // repetidas
	rand.Shuffle(len(deliveries), func(i, j int) { deliveries[i], deliveries[j] = deliveries[j], deliveries[i] })

	var wg sync.WaitGroup
	for _, e := range deliveries {
		wg.Go(func() {
			if _, err := store.Process(ctx, e, ""); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	st, timeline, err := store.Get(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	last := sequence[5]
	if st.Status != order.StatusDelivered || st.Driver == nil || *st.Driver != *last.Position || !st.PositionAt.Equal(last.OccurredAt) {
		t.Fatalf("estado final: %+v", st)
	}
	// linha do tempo: 4 mudanças de status, sem repetição, em ordem cronológica
	if len(timeline) != 4 || timeline[0].Type != events.OrderAssigned || timeline[3].Type != events.OrderDelivered {
		t.Fatalf("linha do tempo: %+v", timeline)
	}
}

func TestProcess_StoresBuyerMessageOnFailedAttempt(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	_, _ = store.Process(ctx, ev(events.OrderOutForDelivery, 0, pt(-23.55, -46.63)), "")
	failed := ev(events.OrderFailedAttempt, 10, pt(-23.5614, -46.6559))
	failed.Note = "cliente ausente"
	if _, err := store.Process(ctx, failed, "Não encontramos ninguém no endereço."); err != nil {
		t.Fatal(err)
	}
	st, _, _ := store.Get(ctx, orderID)
	if st.Status != order.StatusFailedAttempt || st.BuyerMessage != "Não encontramos ninguém no endereço." {
		t.Fatalf("estado: %+v", st)
	}
}

func TestGet_NotFound(t *testing.T) {
	store, _ := newTestStore(t)
	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "nao-e-uuid"} {
		if _, _, err := store.Get(context.Background(), id); err != ErrNotFound {
			t.Errorf("Get(%q) = %v, quero ErrNotFound", id, err)
		}
	}
}
