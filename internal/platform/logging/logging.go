// Package logging cria o logger estruturado (slog em JSON) usado pelos serviços.
//
// Os nomes dos campos seguem o que o Cloud Logging (GCP) entende: "severity" e
// "message". Assim, no Cloud Run, o nível do log aparece certo no console.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New devolve um logger JSON com o campo "service" em todas as linhas.
// O nível vem da variável LOG_LEVEL (debug, info, warn, error); o padrão é info.
func New(service string) *slog.Logger {
	return NewWithWriter(os.Stdout, service, os.Getenv("LOG_LEVEL"))
}

// NewWithWriter é como New, mas escreve em w (usado nos testes).
func NewWithWriter(w io.Writer, service, level string) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       parseLevel(level),
		ReplaceAttr: cloudLoggingKeys,
	})
	return slog.New(h).With("service", service)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// cloudLoggingKeys renomeia "level" para "severity" e "msg" para "message".
func cloudLoggingKeys(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.LevelKey:
		a.Key = "severity"
		if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == slog.LevelWarn {
			a.Value = slog.StringValue("WARNING") // o GCP usa WARNING, não WARN
		}
	case slog.MessageKey:
		a.Key = "message"
	}
	return a
}

// Discard devolve um logger que não escreve nada (usado nos testes).
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }
