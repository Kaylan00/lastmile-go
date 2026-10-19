// Package httpx tem o que os dois serviços usam em HTTP: servidor com timeouts,
// graceful shutdown, middlewares (log, recover, timeout) e helpers de JSON.
package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// NewServer cria um http.Server com timeouts. Sem eles, um cliente lento pode
// segurar uma conexão (e uma goroutine) para sempre.
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// Run abre a porta de srv.Addr e chama Serve.
func Run(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration, log *slog.Logger) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	return Serve(ctx, srv, ln, shutdownTimeout, log)
}

// Serve atende em ln e bloqueia até ctx ser cancelado (SIGTERM/Ctrl+C).
// Aí chama Shutdown: o servidor para de aceitar conexões novas e espera as
// requisições em andamento terminarem, até o limite de shutdownTimeout.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration, log *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		log.Info("servidor http no ar", "addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// ctx já foi cancelado; WithoutCancel cria um contexto que herda os valores
	// mas não o cancelamento, e damos a ele um prazo próprio.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	log.Info("desligando servidor http")
	return srv.Shutdown(shutdownCtx)
}
