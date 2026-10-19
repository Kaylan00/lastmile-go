package httpx

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// Health responde /health e /ready.
//
//   - /health (liveness): o processo está vivo? Sempre 200 enquanto o servidor responde.
//   - /ready (readiness): pode receber tráfego? Checa a dependência (o banco) e
//     devolve 503 durante o desligamento, para o balanceador parar de mandar requisições.
type Health struct {
	ready atomic.Bool // atomic porque é lido e escrito por goroutines diferentes
	check func(context.Context) error
}

// NewHealth cria o Health. check é chamado em cada /ready (ex.: ping no banco).
func NewHealth(check func(context.Context) error) *Health {
	return &Health{check: check}
}

// SetReady liga ou desliga a prontidão.
func (h *Health) SetReady(v bool) { h.ready.Store(v) }

// Register adiciona as duas rotas no mux.
func (h *Health) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		if !h.ready.Load() {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
			return
		}
		if h.check != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := h.check(ctx); err != nil {
				// não devolve err.Error(): a mensagem do banco não deve vazar para fora
				WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "dependency down"})
				return
			}
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
}
