package tracking

import (
	"math"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/eta"
	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/notify"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

// View é a resposta de GET /orders/{id}/tracking: o que a página do comprador mostra.
type View struct {
	OrderID        string          `json:"order_id"`
	Status         order.Status    `json:"status"`
	StatusAt       *time.Time      `json:"status_at,omitempty"`
	DriverID       string          `json:"driver_id,omitempty"`
	Pickup         geo.Point       `json:"pickup"`
	Dropoff        geo.Point       `json:"dropoff"`
	DriverPosition *Position       `json:"driver_position,omitempty"`
	ETA            *ETA            `json:"eta,omitempty"`
	Message        string          `json:"message,omitempty"` // para o comprador, quando a tentativa falha
	Timeline       []TimelineEntry `json:"timeline"`
	Demo           bool            `json:"demo,omitempty"` // true quando vem da demo em memória
}

// Position é a última posição conhecida do motorista.
type Position struct {
	Lat float64   `json:"lat"`
	Lng float64   `json:"lng"`
	At  time.Time `json:"at"`
}

// ETA é a previsão de chegada.
type ETA struct {
	DistanceKm  float64   `json:"distance_km"`
	MinutesLeft int       `json:"minutes_left"`
	Arrival     time.Time `json:"arrival"`
}

// TimelineEntry é uma etapa que já aconteceu.
type TimelineEntry struct {
	Type events.Type `json:"type"`
	At   time.Time   `json:"at"`
}

// BuildView junta o estado, a linha do tempo e a previsão.
func BuildView(s State, timeline []TimelineEntry, params eta.Params, now time.Time) View {
	v := View{
		OrderID:  s.OrderID,
		Status:   s.Status,
		DriverID: s.DriverID,
		Pickup:   s.Pickup,
		Dropoff:  s.Dropoff,
		Message:  s.BuyerMessage,
		Timeline: timeline,
	}
	if v.Timeline == nil {
		v.Timeline = []TimelineEntry{} // no JSON vira [] em vez de null
	}
	if v.Status == order.StatusFailedAttempt && v.Message == "" {
		v.Message = notify.DefaultMessage // o comprador nunca fica sem explicação
	}
	if !s.StatusAt.IsZero() {
		at := s.StatusAt
		v.StatusAt = &at
	}
	if s.Driver != nil {
		v.DriverPosition = &Position{Lat: s.Driver.Lat, Lng: s.Driver.Lng, At: s.PositionAt}
	}
	est, ok := eta.Calculate(eta.Input{
		Status: s.Status, Driver: s.Driver, PositionAt: s.PositionAt, Pickup: s.Pickup, Dropoff: s.Dropoff,
	}, params)
	if ok {
		v.ETA = &ETA{
			DistanceKm:  math.Round(est.DistanceKm*10) / 10,
			MinutesLeft: est.MinutesLeft(now),
			Arrival:     est.Arrival,
		}
	}
	return v
}
