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
	"syscall"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/dispatch"
	"github.com/Kaylan00/lastmile-go/internal/platform/config"
	"github.com/Kaylan00/lastmile-go/internal/platform/httpx"
	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
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
}

func loadConfig(env *config.Env) (Config, error) {
	cfg := Config{
		HTTPAddr:        ":" + env.String("PORT", "8080"), // o Cloud Run define PORT
		DatabaseURL:     env.Required("DATABASE_URL"),
		DBMaxConns:      env.Int("DB_MAX_CONNS", 10),
		RequestTimeout:  env.Duration("REQUEST_TIMEOUT", 5*time.Second),
		ShutdownTimeout: env.Duration("SHUTDOWN_TIMEOUT", 8*time.Second),
	}
	return cfg, env.Err()
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

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns, log)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := postgres.Migrate(ctx, pool, migrations.Dispatch(), log); err != nil {
		return err
	}
	store := dispatch.NewStore(pool)

	health := httpx.NewHealth(pool.Ping)
	mux := http.NewServeMux()
	health.Register(mux)
	dispatch.NewAPI(store, log, nil).Register(mux)

	handler := httpx.Chain(mux, httpx.Recover(log), httpx.Logging(log), httpx.Timeout(cfg.RequestTimeout))
	srv := httpx.NewServer(cfg.HTTPAddr, handler)

	health.SetReady(true)
	go func() {
		<-ctx.Done()
		health.SetReady(false) // /ready passa a dar 503 assim que o desligamento começa
	}()
	return httpx.Run(ctx, srv, cfg.ShutdownTimeout, log)
}
