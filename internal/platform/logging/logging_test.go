package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestCloudLoggingFields(t *testing.T) {
	tests := []struct {
		level        slog.Level
		wantSeverity string
	}{
		{slog.LevelInfo, "INFO"},
		{slog.LevelWarn, "WARNING"}, // o GCP não entende "WARN"
		{slog.LevelError, "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.wantSeverity, func(t *testing.T) {
			var buf bytes.Buffer
			l := NewWithWriter(&buf, "dispatch", "debug")
			l.Log(t.Context(), tt.level, "motorista sem posição", "driver_id", "drv-01")

			var line map[string]any
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("log não é JSON: %v (%s)", err, buf.String())
			}
			if line["severity"] != tt.wantSeverity {
				t.Errorf("severity = %v, quero %s", line["severity"], tt.wantSeverity)
			}
			if line["message"] != "motorista sem posição" {
				t.Errorf("message = %v", line["message"])
			}
			if line["service"] != "dispatch" || line["driver_id"] != "drv-01" {
				t.Errorf("campos faltando: %v", line)
			}
		})
	}
}

func TestLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf, "tracking", "warn")
	l.Info("não deve aparecer")
	if buf.Len() != 0 {
		t.Fatalf("log info apareceu com nível warn: %s", buf.String())
	}
}
