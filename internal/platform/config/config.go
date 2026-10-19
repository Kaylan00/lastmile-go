// Package config lê configurações de variáveis de ambiente (estilo 12-factor).
//
// Em vez de devolver um erro em cada leitura, o Env acumula os erros e devolve
// todos juntos no final com Err(). Assim o Load() de cada serviço fica linear.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Env lê variáveis de ambiente e guarda os erros de conversão.
type Env struct {
	lookup func(string) (string, bool)
	errs   []error
}

// FromOS cria um Env que lê do ambiente do processo.
func FromOS() *Env { return &Env{lookup: os.LookupEnv} }

// FromMap cria um Env que lê de um map (usado nos testes).
func FromMap(m map[string]string) *Env {
	return &Env{lookup: func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}}
}

func (e *Env) raw(key string) (string, bool) {
	v, ok := e.lookup(key)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// String devolve o valor da variável ou def se ela não existir.
func (e *Env) String(key, def string) string {
	if v, ok := e.raw(key); ok {
		return v
	}
	return def
}

// Required devolve o valor da variável e registra erro se ela estiver vazia.
func (e *Env) Required(key string) string {
	v, ok := e.raw(key)
	if !ok {
		e.errs = append(e.errs, fmt.Errorf("%s: obrigatória e não definida", key))
	}
	return v
}

// Int lê um inteiro.
func (e *Env) Int(key string, def int) int {
	v, ok := e.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: %q não é um inteiro", key, v))
		return def
	}
	return n
}

// Float lê um número decimal.
func (e *Env) Float(key string, def float64) float64 {
	v, ok := e.raw(key)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: %q não é um número", key, v))
		return def
	}
	return f
}

// Bool lê true/false (aceita 1, t, true, 0, f, false).
func (e *Env) Bool(key string, def bool) bool {
	v, ok := e.raw(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: %q não é true/false", key, v))
		return def
	}
	return b
}

// Duration lê uma duração no formato do Go ("500ms", "3s", "1m").
func (e *Env) Duration(key string, def time.Duration) time.Duration {
	v, ok := e.raw(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: %q não é uma duração (ex.: 3s)", key, v))
		return def
	}
	return d
}

// Err devolve todos os erros acumulados (ou nil).
func (e *Env) Err() error { return errors.Join(e.errs...) }
