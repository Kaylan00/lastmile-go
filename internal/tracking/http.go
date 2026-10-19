package tracking

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/eta"
	"github.com/Kaylan00/lastmile-go/internal/platform/httpx"
)

// API expõe o rastreio por HTTP.
type API struct {
	store  *Store
	params eta.Params
	log    *slog.Logger
	now    func() time.Time // trocável nos testes
}

// NewAPI cria a API.
func NewAPI(store *Store, params eta.Params, log *slog.Logger) *API {
	return &API{store: store, params: params, log: log, now: time.Now}
}

// Register adiciona as rotas no mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /orders/{id}/tracking", a.getTracking)
}

func (a *API) getTracking(w http.ResponseWriter, r *http.Request) {
	st, timeline, err := a.store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "pedido não encontrado (ou ainda sem nenhum evento)")
		return
	}
	if err != nil {
		a.log.Error("erro ao ler rastreio", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	// A página consulta a cada 5 s; não vale a pena guardar em cache.
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, BuildView(st, timeline, a.params, a.now()))
}
