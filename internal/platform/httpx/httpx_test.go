package httpx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
)

func TestHealth(t *testing.T) {
	tests := []struct {
		name       string
		ready      bool
		checkErr   error
		path       string
		wantStatus int
	}{
		{"health sempre 200", false, nil, "/health", http.StatusOK},
		{"ready antes de subir", false, nil, "/ready", http.StatusServiceUnavailable},
		{"ready com banco ok", true, nil, "/ready", http.StatusOK},
		{"ready com banco fora", true, errors.New("connection refused"), "/ready", http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHealth(func(context.Context) error { return tt.checkErr })
			h.SetReady(tt.ready)
			mux := http.NewServeMux()
			h.Register(mux)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, quero %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestDecodeJSON(t *testing.T) {
	type body struct {
		Lat float64 `json:"lat"`
	}
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"válido", `{"lat": -23.5}`, false},
		{"vazio", ``, true},
		{"campo desconhecido", `{"lat": 1, "latitude": 2}`, true},
		{"dois objetos", `{"lat": 1}{"lat": 2}`, true},
		{"tipo errado", `{"lat": "norte"}`, true},
		{"grande demais", `{"lat": 1, "x": "` + strings.Repeat("a", maxBodyBytes) + `"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.input))
			var dst body
			err := DecodeJSON(httptest.NewRecorder(), r, &dst)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, quero erro? %v", err, tt.wantErr)
			}
		})
	}
}

func TestRecover(t *testing.T) {
	h := Recover(logging.Discard())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("bug")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, quero 500", rec.Code)
	}
}

func TestTimeoutMiddleware(t *testing.T) {
	var deadline time.Time
	h := Timeout(time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		deadline, _ = r.Context().Deadline()
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if deadline.IsZero() {
		t.Fatal("o contexto da requisição deveria ter prazo")
	}
}

// Requisição em andamento quando chega o SIGTERM precisa terminar com 200.
func TestServeGracefulShutdown(t *testing.T) {
	started := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, NewServer("", mux), ln, 5*time.Second, logging.Discard()) }()

	respCh := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			respCh <- 0
			return
		}
		resp.Body.Close()
		respCh <- resp.StatusCode
	}()

	<-started
	cancel() // simula o SIGTERM no meio da requisição

	if code := <-respCh; code != http.StatusOK {
		t.Fatalf("requisição em andamento terminou com %d, quero 200", code)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve devolveu erro: %v", err)
	}
}
