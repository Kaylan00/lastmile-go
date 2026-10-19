package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	p := Policy{Initial: 100 * time.Millisecond, Max: time.Second, Multiplier: 2}
	tests := []struct {
		n    int
		want time.Duration
	}{
		{0, 100 * time.Millisecond}, // n inválido vira 1
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{5, time.Second}, // 1.6s passaria do teto
		{50, time.Second},
	}
	for _, tt := range tests {
		if got := p.Backoff(tt.n); got != tt.want {
			t.Errorf("Backoff(%d) = %v, quero %v", tt.n, got, tt.want)
		}
	}
}

func TestJitterStaysInRange(t *testing.T) {
	p := Policy{Initial: 100 * time.Millisecond, Max: time.Second, Jitter: 0.5}
	for range 1000 {
		w := p.wait(1)
		if w < 50*time.Millisecond || w > 100*time.Millisecond {
			t.Fatalf("espera com jitter %v fora de [50ms, 100ms]", w)
		}
	}
}

func TestDo(t *testing.T) {
	errTemp := errors.New("banco fora do ar")
	fast := Policy{Attempts: 4, Initial: time.Millisecond, Max: 2 * time.Millisecond}

	tests := []struct {
		name      string
		failures  int   // quantas vezes fn falha antes de dar certo
		failWith  error // erro devolvido nas falhas
		wantCalls int
		wantErr   bool
	}{
		{"sucesso de primeira", 0, errTemp, 1, false},
		{"sucesso na terceira", 2, errTemp, 3, false},
		{"esgota as tentativas", 10, errTemp, 4, true},
		{"erro permanente para na hora", 10, Permanent(errors.New("JSON inválido")), 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			err := Do(context.Background(), fast, func(context.Context) error {
				calls++
				if calls <= tt.failures {
					return tt.failWith
				}
				return nil
			})
			if calls != tt.wantCalls {
				t.Errorf("chamadas = %d, quero %d", calls, tt.wantCalls)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, quero erro? %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, tt.failWith) && !IsPermanent(err) {
				t.Errorf("o erro original deveria continuar acessível com errors.Is: %v", err)
			}
		})
	}
}

func TestDoStopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	slow := Policy{Attempts: 100, Initial: time.Hour}
	calls := 0
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := Do(ctx, slow, func(context.Context) error {
		calls++
		return errors.New("falhou")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, quero context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("chamadas = %d, quero 1", calls)
	}
}
