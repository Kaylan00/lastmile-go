package dispatch

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/platform/httpx"
)

// API expõe o dispatch-service por HTTP.
type API struct {
	store *Store
	log   *slog.Logger
	wake  func() // avisa os workers que surgiu trabalho novo (pode ser nil)
}

// NewAPI cria a API. wake é chamado depois de criar pedido ou liberar motorista.
func NewAPI(store *Store, log *slog.Logger, wake func()) *API {
	if wake == nil {
		wake = func() {}
	}
	return &API{store: store, log: log, wake: wake}
}

// Register adiciona as rotas no mux (roteamento com método e {id} do Go 1.22+).
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /orders", a.createOrder)
	mux.HandleFunc("GET /orders/{id}", a.getOrder)
	mux.HandleFunc("POST /drivers/{id}/availability", a.setAvailability)
	mux.HandleFunc("GET /drivers/{id}", a.getDriver)
}

type createOrderRequest struct {
	Pickup  *geo.Point `json:"pickup"`
	Dropoff *geo.Point `json:"dropoff"`
}

func (a *API) createOrder(w http.ResponseWriter, r *http.Request) {
	var req createOrderRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePoint("pickup", req.Pickup); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePoint("dropoff", req.Dropoff); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	o, err := a.store.CreateOrder(r.Context(), *req.Pickup, *req.Dropoff)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.wake()
	a.log.Info("pedido criado", "order_id", o.ID)
	w.Header().Set("Location", "/orders/"+o.ID)
	httpx.WriteJSON(w, http.StatusCreated, o)
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	o, err := a.store.GetOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}

type availabilityRequest struct {
	Available bool       `json:"available"`
	Position  *geo.Point `json:"position"`
}

func (a *API) setAvailability(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validDriverID(id) {
		httpx.WriteError(w, http.StatusBadRequest, "id do motorista inválido (use letras, números, - e _)")
		return
	}
	var req availabilityRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// para ficar livre a posição é obrigatória; para ficar offline é opcional
	if req.Available || req.Position != nil {
		if err := validatePoint("position", req.Position); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	d, err := a.store.SetAvailability(r.Context(), id, req.Available, req.Position)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if d.Status == DriverAvailable {
		a.wake()
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (a *API) getDriver(w http.ResponseWriter, r *http.Request) {
	d, err := a.store.GetDriver(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func validatePoint(field string, p *geo.Point) error {
	if p == nil {
		return fmt.Errorf("%s é obrigatório ({\"lat\": ..., \"lng\": ...})", field)
	}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

// fail traduz os erros do domínio em status HTTP. Erro desconhecido vira 500
// e é registrado no log (o cliente não vê detalhes internos).
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDriverBusy), errors.Is(err, ErrInvalidTransition), errors.Is(err, ErrWrongDriver):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	default:
		a.log.Error("erro na requisição", "path", r.URL.Path, "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "erro interno")
	}
}
