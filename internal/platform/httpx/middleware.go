package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Middleware é uma função que embrulha um handler com comportamento extra.
type Middleware func(http.Handler) http.Handler

// Chain aplica os middlewares na ordem em que aparecem: o primeiro é o mais externo.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// statusRecorder guarda o status que o handler escreveu, para o log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Logging registra uma linha por requisição (método, caminho, status e duração).
// /health e /ready ficam em nível debug para não poluir o log.
func Logging(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			if r.URL.Path == "/health" || r.URL.Path == "/ready" {
				level = slog.LevelDebug
			}
			log.Log(r.Context(), level, "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// Recover transforma um panic num handler em resposta 500, em vez de derrubar o processo.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					if p == http.ErrAbortHandler {
						panic(p) // o próprio net/http usa esse panic para abortar; repassa
					}
					log.Error("panic no handler", "panic", p, "path", r.URL.Path)
					WriteError(w, http.StatusInternalServerError, "erro interno")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout coloca um prazo no contexto de cada requisição. Toda chamada ao banco
// feita com r.Context() herda esse prazo e é cancelada se ele estourar.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
