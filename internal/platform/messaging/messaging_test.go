package messaging

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/pstest"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// fakeServer sobe o Pub/Sub falso da própria biblioteca do Google (pstest), em memória.
func fakeServer(t *testing.T) *pubsub.Client {
	t.Helper()
	srv := pstest.NewServer()
	t.Cleanup(func() { _ = srv.Close() })
	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Connect(context.Background(), "test-project", option.WithGRPCConn(conn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestPublishAndReceive(t *testing.T) {
	c := fakeServer(t)
	ctx := context.Background()
	if err := EnsureTopic(ctx, c, "order-events"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTopic(ctx, c, "order-events"); err != nil {
		t.Fatalf("criar de novo deveria ser ok (idempotente): %v", err)
	}
	if err := EnsureSubscription(ctx, c, SubscriptionConfig{ID: "tracking", TopicID: "order-events", AckDeadline: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	pub := NewPublisher(c, "order-events")
	defer pub.Stop()
	if err := pub.Publish(ctx, Message{Data: []byte(`{"ok":true}`), Attributes: map[string]string{"event_id": "e1"}}); err != nil {
		t.Fatal(err)
	}

	got := make(chan Received, 1)
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	go func() {
		_ = Receive(rctx, c, ReceiveConfig{SubscriptionID: "tracking", MaxOutstanding: 1}, func(_ context.Context, m Received) error {
			got <- m
			cancel()
			return nil
		})
	}()

	select {
	case m := <-got:
		if string(m.Data) != `{"ok":true}` || m.Attributes["event_id"] != "e1" {
			t.Fatalf("mensagem errada: %+v", m)
		}
	case <-rctx.Done():
		t.Fatal("mensagem não chegou")
	}
}

// Um handler que sempre falha faz o Pub/Sub tentar de novo e, depois de
// MaxDeliveryAttempts, mandar a mensagem para o tópico de dead-letter.
func TestNackGoesToDeadLetterAfterMaxAttempts(t *testing.T) {
	c := fakeServer(t)
	ctx := context.Background()
	for _, topic := range []string{"order-events", "order-events-dlq"} {
		if err := EnsureTopic(ctx, c, topic); err != nil {
			t.Fatal(err)
		}
	}
	// a dead-letter precisa de uma assinatura, senão a mensagem é descartada
	if err := EnsureSubscription(ctx, c, SubscriptionConfig{ID: "dlq-inspect", TopicID: "order-events-dlq", AckDeadline: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSubscription(ctx, c, SubscriptionConfig{
		ID: "tracking", TopicID: "order-events", AckDeadline: 10 * time.Second,
		DeadLetterTopicID: "order-events-dlq", MaxDeliveryAttempts: 5,
	}); err != nil {
		t.Fatal(err)
	}

	pub := NewPublisher(c, "order-events")
	defer pub.Stop()
	if err := pub.Publish(ctx, Message{Data: []byte("banco fora do ar")}); err != nil {
		t.Fatal(err)
	}

	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	attempts := []int{}
	go func() {
		_ = Receive(rctx, c, ReceiveConfig{SubscriptionID: "tracking", MaxOutstanding: 1}, func(_ context.Context, m Received) error {
			mu.Lock()
			attempts = append(attempts, m.DeliveryAttempt)
			mu.Unlock()
			return errors.New("falha temporária")
		})
	}()

	dead := make(chan Received, 1)
	go func() {
		_ = Receive(rctx, c, ReceiveConfig{SubscriptionID: "dlq-inspect", MaxOutstanding: 1}, func(_ context.Context, m Received) error {
			dead <- m
			return nil
		})
	}()

	select {
	case m := <-dead:
		if string(m.Data) != "banco fora do ar" {
			t.Fatalf("dead-letter recebeu %q", m.Data)
		}
	case <-rctx.Done():
		t.Fatal("a mensagem não chegou na dead-letter")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 5 || attempts[0] != 1 || attempts[4] != 5 {
		t.Fatalf("tentativas = %v, quero [1 2 3 4 5]", attempts)
	}
}
