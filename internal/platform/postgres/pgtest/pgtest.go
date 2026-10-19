// Package pgtest prepara um banco isolado para os testes de integração.
//
// Cada teste ganha um schema novo no Postgres apontado por TEST_DATABASE_URL,
// com as migrations aplicadas. No fim do teste o schema é apagado. Assim os
// testes não se atrapalham, mesmo rodando em paralelo.
package pgtest

import (
	"context"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
	"github.com/Kaylan00/lastmile-go/internal/platform/postgres"
)

// URL devolve TEST_DATABASE_URL. Sem ela, o teste é pulado; no CI (variável CI
// definida) o teste falha, para o teste de integração nunca ser pulado em silêncio.
func URL(t testing.TB) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL não definida no CI")
		}
		t.Skip("defina TEST_DATABASE_URL para rodar os testes de integração (ex.: make test-integration)")
	}
	return url
}

// New cria um schema exclusivo, aplica as migrations de fsys e devolve um pool
// que enxerga só esse schema. maxConns limita as conexões do pool.
func New(t testing.TB, fsys fs.FS, maxConns int32) *pgxpool.Pool {
	t.Helper()
	url := URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schema := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), rand.IntN(1_000_000))

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("conectando em TEST_DATABASE_URL: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("criando schema: %v", err)
	}
	_ = admin.Close(ctx)

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = maxConns

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if conn, err := pgx.Connect(ctx, url); err == nil {
			_, _ = conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
			_ = conn.Close(ctx)
		}
	})

	if err := postgres.Migrate(ctx, pool, fsys, logging.Discard()); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return pool
}
