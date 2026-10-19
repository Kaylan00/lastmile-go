package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const maxBodyBytes = 1 << 20 // 1 MB: corpo maior que isso é recusado

// WriteJSON responde com status e o valor v em JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // se o cliente desconectou, não há o que fazer
}

// ErrorResponse é o formato de erro de todas as rotas: {"error": "..."}.
type ErrorResponse struct {
	Error string `json:"error"`
}

// WriteError responde {"error": msg} com o status dado.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, ErrorResponse{Error: msg})
}

// DecodeJSON lê o corpo da requisição em dst. Recusa campos desconhecidos,
// corpo vazio, corpo maior que 1 MB e mais de um objeto JSON.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			return errors.New("corpo da requisição vazio")
		case errors.As(err, &maxErr):
			return fmt.Errorf("corpo maior que %d bytes", maxErr.Limit)
		default:
			return fmt.Errorf("JSON inválido: %w", err)
		}
	}
	if dec.More() {
		return errors.New("o corpo deve ter um único objeto JSON")
	}
	return nil
}
