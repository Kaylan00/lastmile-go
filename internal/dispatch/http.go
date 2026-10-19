package dispatch

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
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
	// rotas do app do motorista durante a entrega
	mux.HandleFunc("POST /orders/{id}/events", a.reportProgress)
	mux.HandleFunc("POST /drivers/{id}/location", a.updateLocation)
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

type progressRequest struct {
	DriverID string       `json:"driver_id"`
	Status   order.Status `json:"status"`
	Position *geo.Point   `json:"position"`
	Note     string       `json:"note"`
}

type progressResponse struct {
	Order
	Changed bool `json:"changed"` // false quando a etapa repetiu (retry do app)
}

func (a *API) reportProgress(w http.ResponseWriter, r *http.Request) {
	var req progressRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch {
	case !validDriverID(req.DriverID):
		httpx.WriteError(w, http.StatusBadRequest, "driver_id inválido")
		return
	case !req.Status.Valid() || req.Status == order.StatusPending || req.Status == order.StatusAssigned:
		httpx.WriteError(w, http.StatusBadRequest, "status deve ser picked_up, out_for_delivery, delivered ou failed_attempt")
		return
	case len(req.Note) > maxNoteLen:
		httpx.WriteError(w, http.StatusBadRequest, fmt.Sprintf("note com mais de %d caracteres", maxNoteLen))
		return
	}
	if req.Position != nil {
		if err := validatePoint("position", req.Position); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	o, changed, err := a.store.ReportProgress(r.Context(), Progress{
		OrderID:  r.PathValue("id"),
		DriverID: req.DriverID,
		Status:   req.Status,
		Position: req.Position,
		Note:     req.Note,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if changed {
		a.log.Info("etapa da entrega", "order_id", o.ID, "driver_id", o.DriverID, "status", o.Status)
		if o.Status.Terminal() {
			a.wake() // o motorista ficou livre: pode pegar outro pedido
		}
	}
	httpx.WriteJSON(w, http.StatusOK, progressResponse{Order: o, Changed: changed})
}

type locationRequest struct {
	Position *geo.Point `json:"position"`
}

func (a *API) updateLocation(w http.ResponseWriter, r *http.Request) {
	var req locationRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePoint("position", req.Position); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	d, err := a.store.UpdateLocation(r.Context(), r.PathValue("id"), *req.Position)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

const maxNoteLen = 500

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
