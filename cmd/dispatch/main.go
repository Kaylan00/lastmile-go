// Command dispatch é o dispatch-service: recebe pedidos e motoristas e aloca
// cada pedido ao motorista livre mais próximo.
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

	"github.com/Kaylan00/lastmile-go/internal/dispatch"
	"github.com/Kaylan00/lastmile-go/internal/platform/config"
	"github.com/Kaylan00/lastmile-go/internal/platform/httpx"
	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
	"github.com/Kaylan00/lastmile-go/internal/platform/messaging"
	"github.com/Kaylan00/lastmile-go/internal/platform/postgres"
	"github.com/Kaylan00/lastmile-go/migrations"
)

// Config reúne tudo o que o serviço lê do ambiente.
type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	DBMaxConns      int
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
	Workers         int
	GCPProject      string
	Topic           string
	PubSubSetup     bool
}

func loadConfig(env *config.Env) (Config, error) {
	cfg := Config{
		HTTPAddr:        ":" + env.String("PORT", "8080"), // o Cloud Run define PORT
		DatabaseURL:     env.Required("DATABASE_URL"),
		DBMaxConns:      env.Int("DB_MAX_CONNS", 10),
		RequestTimeout:  env.Duration("REQUEST_TIMEOUT", 5*time.Second),
		ShutdownTimeout: env.Duration("SHUTDOWN_TIMEOUT", 8*time.Second),
		Workers:         env.Int("WORKERS", 4),
		GCPProject:      env.Required("GCP_PROJECT"),
		Topic:           env.String("PUBSUB_TOPIC", "order-events"),
		PubSubSetup:     env.Bool("PUBSUB_SETUP", false), // cria o tópico na subida (emulador/dev)
	}
	if err := env.Err(); err != nil {
		return cfg, err
	}
	// cada worker segura uma conexão durante a alocação; sobra pelo menos 2 para o HTTP e o relay
	if cfg.Workers > cfg.DBMaxConns-2 {
		return cfg, fmt.Errorf("WORKERS (%d) precisa ser no máximo DB_MAX_CONNS-2 (%d)", cfg.Workers, cfg.DBMaxConns-2)
	}
	return cfg, nil
}

func main() {
	log := logging.New("dispatch")

	// ctx é cancelado quando chega SIGTERM (Cloud Run, docker stop) ou Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log); err != nil {
		log.Error("dispatch parou com erro", "err", err)
		os.Exit(1)
	}
	log.Info("dispatch desligado")
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
	if err := postgres.Migrate(ctx, pool, migrations.Dispatch(), log); err != nil {
		return err
	}

	// Pub/Sub
	ps, err := messaging.Connect(ctx, cfg.GCPProject)
	if err != nil {
		return err
	}
	defer ps.Close()
	if cfg.PubSubSetup {
		setupCtx, cancelSetup := context.WithTimeout(ctx, 10*time.Second)
		err := messaging.EnsureTopic(setupCtx, ps, cfg.Topic)
		cancelSetup()
		if err != nil {
			return err
		}
	}
	publisher := messaging.NewPublisher(ps, cfg.Topic)
	defer publisher.Stop() // os defers rodam em ordem inversa: o publisher para antes do client fechar

	// Domínio
	store := dispatch.NewStore(pool)
	workers := dispatch.NewWorkers(store, log, cfg.Workers)
	relay := dispatch.NewRelay(pool, publisher, log)

	// HTTP
	health := httpx.NewHealth(pool.Ping)
	mux := http.NewServeMux()
	health.Register(mux)
	dispatch.NewAPI(store, log, workers.Wake).Register(mux)
	handler := httpx.Chain(mux, httpx.Recover(log), httpx.Logging(log), httpx.Timeout(cfg.RequestTimeout))
	srv := httpx.NewServer(cfg.HTTPAddr, handler)

	// Goroutines de fundo: workers de alocação e relay da outbox.
	var wg sync.WaitGroup
	wg.Go(func() { workers.Run(ctx) })
	wg.Go(func() { relay.Run(ctx) })

	health.SetReady(true)
	go func() {
		<-ctx.Done()
		health.SetReady(false) // /ready passa a dar 503 assim que o desligamento começa
	}()

	// Bloqueia até o SIGTERM. Aí: o servidor HTTP termina as requisições em
	// andamento, os workers terminam a alocação em andamento e o relay para.
	err = httpx.Run(ctx, srv, cfg.ShutdownTimeout, log)
	cancel() // se o servidor parou por erro, derruba o resto também
	wg.Wait()
	return err
}
