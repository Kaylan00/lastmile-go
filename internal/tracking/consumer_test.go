package tracking

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/pstest"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Kaylan00/lastmile-go/internal/eta"
	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/order"
	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
	"github.com/Kaylan00/lastmile-go/internal/platform/messaging"
)

type fakePublisher struct {
	mu   sync.Mutex
	msgs []messaging.Message
}

func (f *fakePublisher) Publish(_ context.Context, m messaging.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, m)
	return nil
}

func (f *fakePublisher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.msgs)
}

func received(t *testing.T, e events.Event) messaging.Received {
	t.Helper()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return messaging.Received{ID: e.ID, Data: data}
}

func TestConsumerHandle(t *testing.T) {
	store, pool := newTestStore(t)
	dlq := &fakePublisher{}
	c := NewConsumer(store, dlq, logging.Discard())
	ctx := context.Background()

	assigned := received(t, ev(events.OrderAssigned, 0, pt(-23.54, -46.62)))
	if err := c.Handle(ctx, assigned); err != nil {
		t.Fatalf("evento válido: %v", err)
	}
	if err := c.Handle(ctx, assigned); err != nil {
		t.Fatalf("duplicado deveria dar ack (nil): %v", err)
	}

	// mensagem inválida: vai para a dead-letter e recebe ack (nil), sem retry
	bad := messaging.Received{ID: "m-1", Data: []byte(`{"type": "order.assigned"}`)}
	if err := c.Handle(ctx, bad); err != nil {
		t.Fatalf("mensagem inválida deveria dar ack depois da dead-letter: %v", err)
	}
	if dlq.count() != 1 || dlq.msgs[0].Attributes["dead_letter_reason"] == "" {
		t.Fatalf("dead-letter: %+v", dlq.msgs)
	}

	// banco fora do ar: erro (nack) para o Pub/Sub tentar de novo depois
	pool.Close()
	if err := c.Handle(ctx, received(t, ev(events.OrderPickedUp, 5, nil))); err == nil {
		t.Fatal("com o banco fechado o Handle deveria devolver erro (nack)")
	}
}

// Teste de integração do consumidor com o Pub/Sub falso do Google (pstest):
// publica os eventos fora de ordem, com uma repetição e uma mensagem inválida,
// e confere o estado final no banco e na API.
func TestConsumerWithPubSub(t *testing.T) {
	store, _ := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	srv := pstest.NewServer()
	defer srv.Close()
	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	client, err := pubsub.NewClient(ctx, "test", option.WithGRPCConn(conn))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, topic := range []string{"order-events", "order-events-dlq"} {
		if err := messaging.EnsureTopic(ctx, client, topic); err != nil {
			t.Fatal(err)
		}
	}
	if err := messaging.EnsureSubscription(ctx, client, messaging.SubscriptionConfig{
		ID: "dlq", TopicID: "order-events-dlq", AckDeadline: 10 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	if err := messaging.EnsureSubscription(ctx, client, messaging.SubscriptionConfig{
		ID: "tracking", TopicID: "order-events", AckDeadline: 10 * time.Second,
		DeadLetterTopicID: "order-events-dlq", MaxDeliveryAttempts: 5,
	}); err != nil {
		t.Fatal(err)
	}

	pub := messaging.NewPublisher(client, "order-events")
	defer pub.Stop()
	assigned := ev(events.OrderAssigned, 0, pt(-23.54, -46.62))
	out := ev(events.OrderOutForDelivery, 6, pt(-23.553, -46.638))
	picked := ev(events.OrderPickedUp, 5, pt(-23.5505, -46.6333))
	for _, e := range []events.Event{out, assigned, picked, out} { // fora de ordem e com repetição
		data, _ := json.Marshal(e)
		if err := pub.Publish(ctx, messaging.Message{Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	if err := pub.Publish(ctx, messaging.Message{Data: []byte("isto não é um evento")}); err != nil {
		t.Fatal(err)
	}

	dlqPub := messaging.NewPublisher(client, "order-events-dlq")
	defer dlqPub.Stop()
	consumer := NewConsumer(store, dlqPub, logging.Discard())

	rctx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() {
		done <- messaging.Receive(rctx, client, messaging.ReceiveConfig{SubscriptionID: "tracking", MaxOutstanding: 4}, consumer.Handle)
	}()

	deadLetters := make(chan messaging.Received, 1)
	go func() {
		_ = messaging.Receive(rctx, client, messaging.ReceiveConfig{SubscriptionID: "dlq", MaxOutstanding: 1}, func(_ context.Context, m messaging.Received) error {
			deadLetters <- m
			return nil
		})
	}()

	select {
	case m := <-deadLetters:
		if string(m.Data) != "isto não é um evento" || m.Attributes["dead_letter_reason"] == "" {
			t.Fatalf("dead-letter recebeu %q %v", m.Data, m.Attributes)
		}
	case <-ctx.Done():
		t.Fatal("a mensagem inválida não chegou na dead-letter")
	}

	// espera os 3 eventos válidos serem aplicados
	for {
		st, timeline, err := store.Get(ctx, orderID)
		if err == nil && len(timeline) == 3 {
			if st.Status != order.StatusOutForDelivery {
				t.Fatalf("status = %s, quero out_for_delivery (o picked_up atrasado não pode voltar o status)", st.Status)
			}
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("eventos não foram aplicados: %+v %v", timeline, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// a API devolve status e ETA
	api := NewAPI(store, eta.DefaultParams, logging.Discard())
	api.now = func() time.Time { return out.OccurredAt }
	mux := http.NewServeMux()
	api.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/orders/"+orderID+"/tracking", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET tracking = %d: %s", rec.Code, rec.Body.String())
	}
	var v View
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Status != order.StatusOutForDelivery || v.ETA == nil || v.ETA.MinutesLeft <= 0 || v.ETA.DistanceKm <= 0 {
		t.Fatalf("resposta da API: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/orders/00000000-0000-0000-0000-000000000000/tracking", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("pedido desconhecido = %d, quero 404", rec.Code)
	}

	stop()
	if err := <-done; err != nil {
		t.Fatalf("Receive terminou com erro: %v", err)
	}
}
