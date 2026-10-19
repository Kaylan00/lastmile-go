package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultModel é o modelo padrão (troque com LLM_MODEL).
const DefaultModel = "claude-opus-5-5"

// Anthropic chama a Messages API da Anthropic (Claude) com net/http puro.
// O projeto usa só a biblioteca padrão onde dá; a chamada é um POST com JSON.
type Anthropic struct {
	apiKey string
	model  string
	url    string // trocável nos testes
	client *http.Client
}

// NewAnthropic cria o client. A chave vem de variável de ambiente, nunca do código.
func NewAnthropic(apiKey, model string) *Anthropic {
	if model == "" {
		model = DefaultModel
	}
	return &Anthropic{
		apiKey: apiKey,
		model:  model,
		url:    "https://api.anthropic.com/v1/messages",
		// rede de segurança: o prazo de verdade (3 s) vem do contexto
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

type messagesRequest struct {
	Model        string        `json:"model"`
	MaxTokens    int           `json:"max_tokens"`
	System       string        `json:"system"`
	Messages     []chatMessage `json:"messages"`
	OutputConfig outputConfig  `json:"output_config"`
	Fallbacks    string        `json:"fallbacks,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type outputConfig struct {
	Effort string `json:"effort"` // "low": a tarefa é simples e o prazo é curto
}

type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
}

// usesFallbacks: nos modelos Opus, um pedido recusado pelos filtros de
// segurança pode ser refeito em outro modelo do lado da Anthropic
// ("fallbacks": "default", recurso beta).
func (a *Anthropic) usesFallbacks() bool { return strings.HasPrefix(a.model, "claude-opus-") }

// Complete manda as instruções (system) e o texto (user) e devolve a resposta.
func (a *Anthropic) Complete(ctx context.Context, system, user string) (string, error) {
	reqBody := messagesRequest{
		Model:        a.model,
		MaxTokens:    1024,
		System:       system,
		Messages:     []chatMessage{{Role: "user", Content: user}},
		OutputConfig: outputConfig{Effort: "low"},
	}
	if a.usesFallbacks() {
		reqBody.Fallbacks = "default"
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	if a.usesFallbacks() {
		req.Header.Set("anthropic-beta", "server-side-fallback-2026-07-01")
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chamando a API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("API respondeu %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var out messagesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("lendo resposta: %w", err)
	}
	switch out.StopReason {
	case "refusal":
		return "", errors.New("o modelo recusou o pedido")
	case "max_tokens":
		return "", errors.New("resposta cortada (max_tokens)")
	}

	// A resposta é uma lista de blocos; só os do tipo "text" são o texto final.
	var sb strings.Builder
	for _, block := range out.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), nil
}
