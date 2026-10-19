package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Servidor HTTP falso no lugar da API: confere o pedido e devolve respostas prontas.
func TestAnthropicComplete(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		delay   time.Duration
		want    string
		wantErr bool
	}{
		{
			name:   "sucesso (ignora blocos que não são texto)",
			status: 200,
			body:   `{"content":[{"type":"thinking","thinking":""},{"type":"text","text":"Não havia ninguém no endereço."}],"stop_reason":"end_turn"}`,
			want:   "Não havia ninguém no endereço.",
		},
		{name: "erro da API", status: 529, body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, wantErr: true},
		{name: "chave inválida", status: 401, body: `{"type":"error","error":{"type":"authentication_error"}}`, wantErr: true},
		{name: "recusa", status: 200, body: `{"content":[],"stop_reason":"refusal"}`, wantErr: true},
		{name: "cortada", status: 200, body: `{"content":[{"type":"text","text":"Passamos no"}],"stop_reason":"max_tokens"}`, wantErr: true},
		{name: "JSON quebrado", status: 200, body: `{"content":`, wantErr: true},
		{name: "lento", status: 200, body: `{"content":[{"type":"text","text":"ok"}]}`, delay: 500 * time.Millisecond, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-api-key") != "chave-de-teste" || r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Errorf("cabeçalhos: %v", r.Header)
				}
				var req messagesRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("corpo do pedido: %v", err)
				}
				if req.Model != DefaultModel || req.System == "" || len(req.Messages) != 1 || req.Messages[0].Role != "user" {
					t.Errorf("pedido: %+v", req)
				}
				if req.Fallbacks != "default" || r.Header.Get("anthropic-beta") == "" {
					t.Errorf("modelo Opus deveria pedir fallbacks: %+v %v", req, r.Header)
				}
				select {
				case <-time.After(tt.delay):
				case <-r.Context().Done():
					return
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			a := NewAnthropic("chave-de-teste", "")
			a.url = srv.URL
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			got, err := a.Complete(ctx, "regras", "<ocorrencia>cliente ausente</ocorrencia>")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, quero erro? %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("texto = %q, quero %q", got, tt.want)
			}
		})
	}
}

func TestAnthropicNoFallbacksForOtherModels(t *testing.T) {
	if NewAnthropic("k", "claude-haiku-5-5").usesFallbacks() {
		t.Fatal("fallbacks só nos modelos Opus")
	}
}
