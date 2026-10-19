package dispatch

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
	"github.com/Kaylan00/lastmile-go/internal/platform/messaging"
)

// fakePublisher guarda as mensagens em memória e pode simular o Pub/Sub fora do ar.
type fakePublisher struct {
	mu   sync.Mutex
	msgs []messaging.Message
	fail error
}

func (f *fakePublisher) Publish(_ context.Context, m messaging.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.msgs = append(f.msgs, m)
	return nil
}

func (f *fakePublisher) setFail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

func (f *fakePublisher) published() []messaging.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]messaging.Message(nil), f.msgs...)
}

func countUnpublished(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAssignNextWritesEventToOutbox(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()
	o, _ := store.CreateOrder(ctx, se, paulista)
	_, _ = store.SetAvailability(ctx, "drv-01", true, &pinheiro)
	if _, err := store.AssignNext(ctx); err != nil {
		t.Fatal(err)
	}

	var payload []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM outbox WHERE order_id = $1 AND event_type = 'order.assigned'`, o.ID).Scan(&payload); err != nil {
		t.Fatalf("evento não foi para a outbox: %v", err)
	}
	e, err := events.Parse(payload)
	if err != nil {
		t.Fatalf("payload inválido: %v", err)
	}
	if e.Type != events.OrderAssigned || e.DriverID != "drv-01" || e.Position == nil || *e.Position != pinheiro {
		t.Fatalf("evento errado: %+v", e)
	}
	if e.Pickup != se || e.Dropoff != paulista {
		t.Fatalf("evento sem coleta/entrega: %+v", e)
	}
}

func TestRelayPublishesPendingEvents(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"drv-01", "drv-02", "drv-03"} {
		_, _ = store.SetAvailability(ctx, id, true, &se)
		_, _ = store.CreateOrder(ctx, se, paulista)
		if _, err := store.AssignNext(ctx); err != nil {
			t.Fatal(err)
		}
	}

	pub := &fakePublisher{}
	relay := NewRelay(pool, pub, logging.Discard())
	n, err := relay.PublishPending(ctx)
	if err != nil || n != 6 { // 3 pedidos x (order.created + order.assigned)
		t.Fatalf("PublishPending = %d, %v; quero 6, nil", n, err)
	}
	if got := countUnpublished(t, pool); got != 0 {
		t.Fatalf("%d eventos continuam sem published_at", got)
	}
	msgs := pub.published()
	if msgs[0].Attributes["event_type"] != string(events.OrderCreated) || msgs[0].Attributes["event_id"] == "" {
		t.Fatalf("atributos da mensagem: %v", msgs[0].Attributes)
	}

	// rodar de novo não publica nada outra vez
	if n, err := relay.PublishPending(ctx); err != nil || n != 0 {
		t.Fatalf("segunda rodada = %d, %v; quero 0, nil", n, err)
	}
}

// O cenário da pergunta "gravei no banco e o Pub/Sub caiu": o evento não se perde.
func TestRelayKeepsEventsWhilePubSubIsDown(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()
	_, _ = store.SetAvailability(ctx, "drv-01", true, &se)
	_, _ = store.CreateOrder(ctx, se, paulista)
	if _, err := store.AssignNext(ctx); err != nil {
		t.Fatal(err)
	}

	pub := &fakePublisher{fail: errors.New("pubsub: unavailable")}
	relay := NewRelay(pool, pub, logging.Discard())

	if _, err := relay.PublishPending(ctx); err == nil {
		t.Fatal("esperava erro com o Pub/Sub fora do ar")
	}
	if got := countUnpublished(t, pool); got != 2 {
		t.Fatalf("o evento deveria continuar na outbox, sem published_at (pendentes = %d)", got)
	}
	var attempts int
	var lastErr string
	_ = pool.QueryRow(ctx, `SELECT attempts, last_error FROM outbox ORDER BY id LIMIT 1`).Scan(&attempts, &lastErr)
	if attempts != 1 || lastErr == "" {
		t.Fatalf("attempts=%d last_error=%q; quero 1 e a mensagem do erro", attempts, lastErr)
	}

	pub.setFail(nil) // o Pub/Sub voltou
	if n, err := relay.PublishPending(ctx); err != nil || n != 2 {
		t.Fatalf("depois de voltar: %d, %v; quero 2, nil", n, err)
	}
	if got := countUnpublished(t, pool); got != 0 {
		t.Fatalf("pendentes = %d, quero 0", got)
	}
}
