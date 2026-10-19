package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationLockID é um número qualquer, fixo, usado no pg_advisory_lock.
const migrationLockID = 7_202_610_19

// Migrate aplica, em ordem alfabética, os arquivos .sql de fsys que ainda não
// foram aplicados. Cada arquivo roda numa transação: ou entra inteiro, ou nada.
//
// Se duas réplicas subirem ao mesmo tempo, o advisory lock faz uma esperar a
// outra terminar, e a segunda vê que não há nada novo para aplicar.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, log *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: pegando conexão: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("migrate: lock: %w", err)
	}
	defer func() {
		// WithoutCancel: mesmo se ctx já tiver sido cancelado, o lock precisa ser solto
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("migrate: criando schema_migrations: %w", err)
	}

	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		var applied bool
		if err := conn.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", name,
		).Scan(&applied); err != nil {
			return fmt.Errorf("migrate: consultando %s: %w", name, err)
		}
		if applied {
			continue
		}

		sql, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		// Exec sem argumentos usa o protocolo simples do Postgres, que aceita
		// vários comandos SQL no mesmo texto.
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate: aplicando %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate: registrando %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrate: commit de %s: %w", name, err)
		}
		log.Info("migration aplicada", "version", name)
	}
	return nil
}
