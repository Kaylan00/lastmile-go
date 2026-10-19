package config

import (
	"testing"
	"time"
)

func TestEnv(t *testing.T) {
	env := FromMap(map[string]string{
		"WORKERS":  "8",
		"TIMEOUT":  "3s",
		"SPEED":    "25.5",
		"ENABLED":  "true",
		"NAME":     "dispatch",
		"BAD_INT":  "oito",
		"BAD_TIME": "3 segundos",
	})

	if got := env.Int("WORKERS", 4); got != 8 {
		t.Errorf("Int = %d, quero 8", got)
	}
	if got := env.Int("MISSING", 4); got != 4 {
		t.Errorf("Int com default = %d, quero 4", got)
	}
	if got := env.Duration("TIMEOUT", time.Second); got != 3*time.Second {
		t.Errorf("Duration = %v, quero 3s", got)
	}
	if got := env.Float("SPEED", 20); got != 25.5 {
		t.Errorf("Float = %v, quero 25.5", got)
	}
	if got := env.Bool("ENABLED", false); !got {
		t.Errorf("Bool = false, quero true")
	}
	if got := env.String("NAME", "x"); got != "dispatch" {
		t.Errorf("String = %q, quero dispatch", got)
	}
	if err := env.Err(); err != nil {
		t.Fatalf("não esperava erro ainda: %v", err)
	}

	env.Int("BAD_INT", 1)
	env.Duration("BAD_TIME", time.Second)
	env.Required("DATABASE_URL")
	if err := env.Err(); err == nil {
		t.Fatal("esperava erro com valores inválidos e variável obrigatória faltando")
	}
}
