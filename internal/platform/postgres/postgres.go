// Package postgres abre o pool de conexões com o PostgreSQL (pgx).
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Kaylan00/lastmile-go/internal/platform/retry"
)

// Connect cria o pool e espera o banco responder. No docker-compose o serviço
// pode subir antes do Postgres aceitar conexões, por isso o ping tem retry.
func Connect(ctx context.Context, url string, maxConns int, log *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL inválida: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = int32(maxConns)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("criando pool: %w", err)
	}

	policy := retry.Policy{Attempts: 10, Initial: 500 * time.Millisecond, Max: 5 * time.Second, Jitter: 0.2}
	err = retry.Do(ctx, policy, func(ctx context.Context) error {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			log.Warn("postgres ainda não respondeu", "err", err)
			return err
		}
		return nil
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("conectando no postgres: %w", err)
	}
	log.Info("postgres conectado", "max_conns", cfg.MaxConns)
	return pool, nil
}
