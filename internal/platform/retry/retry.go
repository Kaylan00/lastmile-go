// Package retry repete uma operação que falhou, esperando cada vez mais entre as
// tentativas (backoff exponencial com jitter).
//
// Exemplo: Initial 100ms, Multiplier 2, Max 2s => espera ~100ms, 200ms, 400ms,
// 800ms, 1.6s, 2s, 2s... O jitter sorteia um pedaço da espera para que várias
// réplicas que falharam juntas não tentem de novo todas no mesmo instante.
package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// Policy define quantas vezes tentar e quanto esperar entre as tentativas.
type Policy struct {
	Attempts   int           // total de tentativas, contando a primeira (mínimo 1)
	Initial    time.Duration // espera depois da 1ª falha
	Max        time.Duration // teto da espera
	Multiplier float64       // quanto a espera cresce a cada falha (padrão 2)
	Jitter     float64       // fração da espera que é sorteada, de 0 a 1 (0 = sem jitter)
}

// Backoff devolve a espera, sem jitter, depois da n-ésima falha (n começa em 1).
func (p Policy) Backoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	mult := p.Multiplier
	if mult < 1 {
		mult = 2
	}
	d := float64(p.Initial)
	for i := 1; i < n; i++ {
		d *= mult
		if p.Max > 0 && d >= float64(p.Max) {
			return p.Max
		}
	}
	if p.Max > 0 && d > float64(p.Max) {
		return p.Max
	}
	return time.Duration(d)
}

// wait aplica o jitter: tira da espera um pedaço sorteado de até Jitter*espera.
func (p Policy) wait(n int) time.Duration {
	d := p.Backoff(n)
	if p.Jitter <= 0 {
		return d
	}
	j := min(p.Jitter, 1)
	return d - time.Duration(rand.Float64()*j*float64(d))
}

// permanentError marca um erro que não adianta repetir (ex.: dado inválido).
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent embrulha err para que Do pare na hora, sem novas tentativas.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent diz se err (ou algum erro dentro dele) foi marcado com Permanent.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

// Do chama fn até dar certo, acabarem as tentativas, ctx ser cancelado ou fn
// devolver um erro Permanent. Devolve nil ou o último erro.
func Do(ctx context.Context, p Policy, fn func(context.Context) error) error {
	attempts := max(p.Attempts, 1)
	var err error
	for n := 1; n <= attempts; n++ {
		if err = fn(ctx); err == nil {
			return nil
		}
		if IsPermanent(err) {
			return err
		}
		if n == attempts {
			break
		}
		t := time.NewTimer(p.wait(n))
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("retry cancelado após %d tentativa(s): %w", n, errors.Join(ctx.Err(), err))
		case <-t.C:
		}
	}
	return fmt.Errorf("desistiu após %d tentativas: %w", attempts, err)
}
