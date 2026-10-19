package demo

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
	"github.com/Kaylan00/lastmile-go/internal/platform/httpx"
)

// NewHandler devolve as rotas da demo, com os mesmos caminhos que a página usa
// no docker-compose:
//
//	POST /api/orders                 cria um pedido de demonstração
//	GET  /api/orders/{id}/tracking   status, ETA e linha do tempo
//	GET  /api/health
func NewHandler() http.Handler {
	h := &handler{now: time.Now}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/orders", h.createOrder)
	mux.HandleFunc("GET /api/orders/{id}/tracking", h.getTracking)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "demo": true})
	})
	return mux
}

type handler struct {
	now func() time.Time
}

type createRequest struct {
	Pickup  *geo.Point `json:"pickup"`
	Dropoff *geo.Point `json:"dropoff"`
}

type createResponse struct {
	ID        string       `json:"id"`
	Status    order.Status `json:"status"`
	Pickup    geo.Point    `json:"pickup"`
	Dropoff   geo.Point    `json:"dropoff"`
	CreatedAt time.Time    `json:"created_at"`
	Demo      bool         `json:"demo"`
}

func (h *handler) createOrder(w http.ResponseWriter, r *http.Request) {
	now := h.now()
	var req createRequest
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	var o Order
	if req.Pickup == nil || req.Dropoff == nil {
		o = RandomOrder(now, rand.New(rand.NewPCG(uint64(now.UnixNano()), rand.Uint64())))
	} else {
		var err error
		o, err = NewOrder(now, *req.Pickup, *req.Dropoff, rand.Uint32())
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	httpx.WriteJSON(w, http.StatusCreated, createResponse{
		ID: o.ID(), Status: order.StatusPending, Pickup: o.Pickup, Dropoff: o.Dropoff, CreatedAt: o.CreatedAt, Demo: true,
	})
}

func (h *handler) getTracking(w http.ResponseWriter, r *http.Request) {
	o, err := ParseID(strings.TrimSpace(r.PathValue("id")))
	now := h.now()
	if errors.Is(err, ErrInvalidID) || (err == nil && o.Expired(now)) {
		httpx.WriteError(w, http.StatusNotFound,
			"pedido não encontrado. Na demo, os pedidos começam com \"demo-\" e valem por 24 h: crie um novo.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, o.View(now))
}
