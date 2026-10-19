// Command tracking é o tracking-service: consome os eventos da entrega e
// responde o status e a previsão de chegada de cada pedido.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub/v2"

	"github.com/Kaylan00/lastmile-go/internal/eta"
	"github.com/Kaylan00/lastmile-go/internal/platform/config"
	"github.com/Kaylan00/lastmile-go/internal/platform/httpx"
	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
	"github.com/Kaylan00/lastmile-go/internal/platform/messaging"
	"github.com/Kaylan00/lastmile-go/internal/platform/postgres"
	"github.com/Kaylan00/lastmile-go/internal/tracking"
	"github.com/Kaylan00/lastmile-go/migrations"
)

// Config reúne tudo o que o serviço lê do ambiente.
type Config struct {
	HTTPAddr            string
	DatabaseURL         string
	DBMaxConns          int
	RequestTimeout      time.Duration
	ShutdownTimeout     time.Duration
	GCPProject          string
	Topic               string
	Subscription        string
	DeadLetterTopic     string
	MaxDeliveryAttempts int
	Concurrency         int
	PubSubSetup         bool
	ETA                 eta.Params
}

func loadConfig(env *config.Env) (Config, error) {
	cfg := Config{
		HTTPAddr:            ":" + env.String("PORT", "8081"),
		DatabaseURL:         env.Required("DATABASE_URL"),
		DBMaxConns:          env.Int("DB_MAX_CONNS", 10),
		RequestTimeout:      env.Duration("REQUEST_TIMEOUT", 5*time.Second),
		ShutdownTimeout:     env.Duration("SHUTDOWN_TIMEOUT", 8*time.Second),
		GCPProject:          env.Required("GCP_PROJECT"),
		Topic:               env.String("PUBSUB_TOPIC", "order-events"),
		Subscription:        env.String("PUBSUB_SUBSCRIPTION", "tracking-order-events"),
		DeadLetterTopic:     env.String("PUBSUB_DEAD_LETTER_TOPIC", "order-events-dlq"),
		MaxDeliveryAttempts: env.Int("PUBSUB_MAX_DELIVERY_ATTEMPTS", 5),
		Concurrency:         env.Int("CONSUMER_CONCURRENCY", 8),
		PubSubSetup:         env.Bool("PUBSUB_SETUP", false),
		ETA: eta.Params{
			SpeedKmh:    env.Float("ETA_SPEED_KMH", eta.DefaultParams.SpeedKmh),
			RouteFactor: env.Float("ETA_ROUTE_FACTOR", eta.DefaultParams.RouteFactor),
		},
	}
	return cfg, env.Err()
}

func main() {
	log := logging.New("tracking")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log); err != nil {
		log.Error("tracking parou com erro", "err", err)
		os.Exit(1)
	}
	log.Info("tracking desligado")
}

func run(ctx context.Context, log *slog.Logger) error {
	cfg, err := loadConfig(config.FromOS())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Banco
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool, migrations.Tracking(), log); err != nil {
		return err
	}

	// Pub/Sub
	ps, err := messaging.Connect(ctx, cfg.GCPProject)
	if err != nil {
		return err
	}
	defer ps.Close()
	if cfg.PubSubSetup {
		if err := setupPubSub(ctx, ps, cfg); err != nil {
			return err
		}
	}
	dlq := messaging.NewPublisher(ps, cfg.DeadLetterTopic)
	defer dlq.Stop()

	// Domínio
	store := tracking.NewStore(pool)
	consumer := tracking.NewConsumer(store, dlq, log)

	// HTTP
	health := httpx.NewHealth(pool.Ping)
	mux := http.NewServeMux()
	health.Register(mux)
	tracking.NewAPI(store, cfg.ETA, log).Register(mux)
	handler := httpx.Chain(mux, httpx.Recover(log), httpx.Logging(log), httpx.Timeout(cfg.RequestTimeout))
	srv := httpx.NewServer(cfg.HTTPAddr, handler)

	// Consumidor do Pub/Sub em segundo plano.
	var wg sync.WaitGroup
	var consumeErr error
	wg.Go(func() {
		log.Info("consumindo eventos", "subscription", cfg.Subscription, "concurrency", cfg.Concurrency)
		consumeErr = messaging.Receive(ctx, ps, messaging.ReceiveConfig{
			SubscriptionID:  cfg.Subscription,
			MaxOutstanding:  cfg.Concurrency,
			ShutdownTimeout: cfg.ShutdownTimeout,
		}, consumer.Handle)
		if consumeErr != nil {
			log.Error("consumidor parou", "err", consumeErr)
			cancel() // sem consumidor o serviço não faz sentido: derruba tudo e deixa reiniciar
		}
	})

	health.SetReady(true)
	go func() {
		<-ctx.Done()
		health.SetReady(false)
	}()

	err = httpx.Run(ctx, srv, cfg.ShutdownTimeout, log)
	cancel()
	wg.Wait() // espera as mensagens em processamento terminarem
	if err == nil {
		err = consumeErr
	}
	return err
}

// setupPubSub cria tópicos e assinaturas se não existirem (emulador e dev).
// Na nuvem isso é feito pelo script deploy/cloudrun.sh.
func setupPubSub(ctx context.Context, ps *pubsub.Client, cfg Config) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, topic := range []string{cfg.Topic, cfg.DeadLetterTopic} {
		if err := messaging.EnsureTopic(ctx, ps, topic); err != nil {
			return err
		}
	}
	// Um tópico sem assinatura descarta as mensagens. Esta assinatura guarda o
	// que cair na dead-letter para alguém investigar.
	if err := messaging.EnsureSubscription(ctx, ps, messaging.SubscriptionConfig{
		ID: cfg.DeadLetterTopic + "-inspect", TopicID: cfg.DeadLetterTopic, AckDeadline: 60 * time.Second,
	}); err != nil {
		return err
	}
	return messaging.EnsureSubscription(ctx, ps, messaging.SubscriptionConfig{
		ID:                  cfg.Subscription,
		TopicID:             cfg.Topic,
		AckDeadline:         30 * time.Second,
		MinBackoff:          time.Second,
		MaxBackoff:          time.Minute,
		DeadLetterTopicID:   cfg.DeadLetterTopic,
		MaxDeliveryAttempts: cfg.MaxDeliveryAttempts,
	})
}
