package dispatch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
)

func doRequest(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// Validação acontece antes de tocar no banco, então dá para testar sem Postgres.
func TestAPIValidation(t *testing.T) {
	mux := http.NewServeMux()
	NewAPI(nil, logging.Discard(), nil).Register(mux)

	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"pedido sem dropoff", "POST", "/orders", `{"pickup": {"lat": -23.5, "lng": -46.6}}`, 400},
		{"pedido com latitude impossível", "POST", "/orders", `{"pickup": {"lat": 123, "lng": -46.6}, "dropoff": {"lat": -23.5, "lng": -46.6}}`, 400},
		{"pedido com campo desconhecido", "POST", "/orders", `{"origem": {}}`, 400},
		{"livre sem posição", "POST", "/drivers/drv-01/availability", `{"available": true}`, 400},
		{"id de motorista inválido", "POST", "/drivers/drv%20x/availability", `{"available": false}`, 400},
		{"método errado", "DELETE", "/orders", ``, 405},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, mux, tt.method, tt.path, tt.body)
			if rec.Code != tt.want {
				t.Errorf("status = %d, quero %d (corpo: %s)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestAPIOrderAndDriverFlow(t *testing.T) {
	store, _ := newTestStore(t)
	woke := 0
	mux := http.NewServeMux()
	NewAPI(store, logging.Discard(), func() { woke++ }).Register(mux)

	rec := doRequest(t, mux, "POST", "/orders", `{"pickup": {"lat": -23.5505, "lng": -46.6333}, "dropoff": {"lat": -23.5614, "lng": -46.6559}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /orders = %d: %s", rec.Code, rec.Body.String())
	}
	var created Order
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if rec.Header().Get("Location") != "/orders/"+created.ID {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}

	if rec := doRequest(t, mux, "GET", "/orders/"+created.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /orders/{id} = %d", rec.Code)
	}
	if rec := doRequest(t, mux, "GET", "/orders/nao-existe", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET pedido inexistente = %d, quero 404", rec.Code)
	}

	rec = doRequest(t, mux, "POST", "/drivers/drv-07/availability", `{"available": true, "position": {"lat": -23.55, "lng": -46.64}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("availability = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(t, mux, "GET", "/drivers/drv-07", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /drivers/{id} = %d", rec.Code)
	}
	if woke != 2 {
		t.Errorf("workers acordados %d vezes, quero 2 (pedido novo + motorista livre)", woke)
	}
}
