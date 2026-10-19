package dispatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
)

func TestWorkersAssignEverythingAndStopOnShutdown(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()
	for i := range 5 {
		pos := geo.Point{Lat: se.Lat + float64(i)*0.01, Lng: se.Lng}
		_, _ = store.SetAvailability(ctx, fmt.Sprintf("drv-%d", i), true, &pos)
	}
	for range 8 { // mais pedidos que motoristas: 3 ficam esperando
		_, _ = store.CreateOrder(ctx, se, paulista)
	}

	w := NewWorkers(store, logging.Discard(), 3)
	w.Idle = 20 * time.Millisecond
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		w.Run(runCtx)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var assigned int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE status = 'assigned'`).Scan(&assigned)
		if assigned == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("só %d pedidos alocados depois de 5 s", assigned)
		}
		time.Sleep(20 * time.Millisecond)
	}

	stop() // SIGTERM
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run não terminou depois do cancelamento")
	}
}

func TestWakeNeverBlocks(t *testing.T) {
	w := NewWorkers(nil, logging.Discard(), 1)
	for range 100 {
		w.Wake() // canal com buffer 1: os avisos extras são descartados, sem travar
	}
	if len(w.wake) != 1 {
		t.Fatalf("avisos pendentes = %d, quero 1", len(w.wake))
	}
}
