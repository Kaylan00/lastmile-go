package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/platform/retry"
)

// Workers é o pool de goroutines que aloca pedidos.
//
// O número de goroutines é FIXO (Size). Se o banco ficar lento, as alocações
// demoram mais, mas não nascem goroutines novas: o trabalho espera na tabela
// orders (status pending), que é a fila. Cada alocação tem prazo (Timeout), e
// depois de erros seguidos o worker espera cada vez mais (backoff).
type Workers struct {
	store *Store
	log   *slog.Logger

	Size    int           // quantas goroutines (não passe de DB_MAX_CONNS)
	Idle    time.Duration // espera quando não há trabalho
	Timeout time.Duration // prazo de cada alocação
	Backoff retry.Policy  // espera depois de erros seguidos

	// wake tem buffer 1: Wake() nunca bloqueia, e vários avisos seguidos
	// viram um só (se já tem aviso esperando, o novo é descartado).
	wake chan struct{}
}

// NewWorkers cria o pool com valores padrão.
func NewWorkers(store *Store, log *slog.Logger, size int) *Workers {
	return &Workers{
		store:   store,
		log:     log,
		Size:    max(size, 1),
		Idle:    time.Second,
		Timeout: 5 * time.Second,
		Backoff: retry.Policy{Initial: 200 * time.Millisecond, Max: 10 * time.Second},
		wake:    make(chan struct{}, 1),
	}
}

// Wake acorda um worker parado (chamado quando chega pedido novo ou motorista
// fica livre). Só vale para esta réplica; as outras acham o trabalho no
// próximo ciclo (Idle).
func (w *Workers) Wake() {
	select {
	case w.wake <- struct{}{}:
	default: // já tem um aviso esperando; não precisa de outro
	}
}

// Run sobe as goroutines e bloqueia até ctx ser cancelado E todas terminarem
// a alocação em andamento. É isso que o graceful shutdown espera.
func (w *Workers) Run(ctx context.Context) {
	w.log.Info("workers de alocação iniciados", "size", w.Size)
	var wg sync.WaitGroup
	for i := range w.Size {
		wg.Go(func() { w.loop(ctx, i) })
	}
	wg.Wait()
	w.log.Info("workers de alocação parados")
}

func (w *Workers) loop(ctx context.Context, id int) {
	failures := 0
	for {
		if ctx.Err() != nil {
			return
		}

		// WithoutCancel: se o SIGTERM chegar no meio, a transação termina
		// (commit ou rollback) em vez de ser cortada; o prazo continua valendo.
		actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.Timeout)
		a, err := w.store.AssignNext(actx)
		cancel()

		var wait time.Duration
		switch {
		case err == nil:
			failures = 0
			w.log.Info("pedido alocado", "worker", id, "order_id", a.OrderID, "driver_id", a.DriverID,
				"distance_km", round2(a.DistanceKm))
			continue // pode ter mais pedido na fila: tenta de novo já
		case errors.Is(err, ErrNoPendingOrder), errors.Is(err, ErrNoDriverAvailable):
			failures = 0
			wait = jitter(w.Idle) // nada a fazer agora
		default:
			failures++
			wait = w.Backoff.Backoff(failures)
			w.log.Error("falha na alocação", "worker", id, "err", err, "retry_in", wait.String())
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-w.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// jitter devolve d +- 20%, para as réplicas não consultarem o banco no mesmo instante.
func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
