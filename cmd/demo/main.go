// Command demo sobe a página de rastreio com a API de demonstração em memória,
// sem Docker, Postgres nem Pub/Sub. É o mesmo código da demo publicada na Vercel.
//
//	cd web && npm ci && npm run build && cd ..
//	go run ./cmd/demo            # http://localhost:8090
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/demo"
	"github.com/Kaylan00/lastmile-go/internal/platform/config"
	"github.com/Kaylan00/lastmile-go/internal/platform/httpx"
	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
)

func main() {
	log := logging.New("demo")
	env := config.FromOS()
	addr := ":" + env.String("PORT", "8090")
	static := env.String("WEB_DIR", "web/dist")

	mux := http.NewServeMux()
	mux.Handle("/api/", demo.NewHandler())
	mux.Handle("/", http.FileServer(http.Dir(static)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := httpx.NewServer(addr, httpx.Chain(mux, httpx.Recover(log), httpx.Logging(log)))
	if err := httpx.Run(ctx, srv, 5*time.Second, log); err != nil {
		log.Error("demo parou com erro", "err", err)
		os.Exit(1)
	}
}
