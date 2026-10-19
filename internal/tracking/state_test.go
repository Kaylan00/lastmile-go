package tracking

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

const orderID = "7f1c0e7e-1f4b-4b8e-9d55-3f6f2a8f8a10"

var (
	t0      = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	pickup  = geo.Point{Lat: -23.5505, Lng: -46.6333}
	dropoff = geo.Point{Lat: -23.5614, Lng: -46.6559}
)

// ev monta um evento do pedido de teste, `min` minutos depois de t0.
func ev(typ events.Type, min int, pos *geo.Point) events.Event {
	return events.Event{
		ID: events.NewID(), Type: typ, OrderID: orderID, DriverID: "drv-01",
		OccurredAt: t0.Add(time.Duration(min) * time.Minute),
		Position:   pos, Pickup: pickup, Dropoff: dropoff,
	}
}

func pt(lat, lng float64) *geo.Point { return &geo.Point{Lat: lat, Lng: lng} }

func TestApply(t *testing.T) {
	assigned := ev(events.OrderAssigned, 0, pt(-23.54, -46.62))
	picked := ev(events.OrderPickedUp, 5, pt(-23.5505, -46.6333))
	out := ev(events.OrderOutForDelivery, 6, nil)
	moved := ev(events.OrderLocation, 9, pt(-23.556, -46.645))
	delivered := ev(events.OrderDelivered, 15, pt(-23.5614, -46.6559))

	tests := []struct {
		name       string
		sequence   []events.Event
		wantStatus order.Status
		wantPos    *geo.Point
		lastChange bool // o último evento da sequência mudou alguma coisa?
	}{
		{"ordem certa", []events.Event{assigned, picked, out, moved, delivered}, order.StatusDelivered, delivered.Position, true},
		{"primeiro evento não é o assigned", []events.Event{picked}, order.StatusPickedUp, picked.Position, true},
		{"duplicado não muda nada", []events.Event{assigned, picked, picked}, order.StatusPickedUp, picked.Position, false},
		{"status não volta", []events.Event{assigned, out, picked}, order.StatusOutForDelivery, picked.Position, true},
		{"posição antiga atrasada é ignorada", []events.Event{assigned, moved, picked}, order.StatusPickedUp, moved.Position, true},
		{"nada muda depois de entregue", []events.Event{delivered, out}, order.StatusDelivered, delivered.Position, false},
		{"localização não muda status", []events.Event{assigned, moved}, order.StatusAssigned, moved.Position, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s State
			var changed bool
			for _, e := range tt.sequence {
				s, changed = Apply(s, e)
			}
			if s.Status != tt.wantStatus {
				t.Errorf("status = %s, quero %s", s.Status, tt.wantStatus)
			}
			if s.Driver == nil || *s.Driver != *tt.wantPos {
				t.Errorf("posição = %v, quero %v", s.Driver, tt.wantPos)
			}
			if changed != tt.lastChange {
				t.Errorf("changed no último evento = %v, quero %v", changed, tt.lastChange)
			}
			if s.Pickup != pickup || s.Dropoff != dropoff || s.DriverID != "drv-01" {
				t.Errorf("dados do pedido errados: %+v", s)
			}
		})
	}
}

// O Pub/Sub pode entregar em qualquer ordem e repetir mensagens. Em todas as
// 500 ordens sorteadas (com repetições), o estado final tem que ser o mesmo.
func TestApply_AnyOrderSameResult(t *testing.T) {
	sequence := []events.Event{
		ev(events.OrderAssigned, 0, pt(-23.54, -46.62)),
		ev(events.OrderLocation, 2, pt(-23.545, -46.625)),
		ev(events.OrderPickedUp, 5, pt(-23.5505, -46.6333)),
		ev(events.OrderOutForDelivery, 6, nil),
		ev(events.OrderLocation, 9, pt(-23.556, -46.645)),
		ev(events.OrderDelivered, 15, pt(-23.5614, -46.6559)),
	}
	var want State
	for _, e := range sequence {
		want, _ = Apply(want, e)
	}

	rng := rand.New(rand.NewPCG(1, 2)) // semente fixa: o teste é reproduzível
	for i := range 500 {
		shuffled := append([]events.Event(nil), sequence...)
		for range rng.IntN(4) { // até 3 mensagens repetidas
			shuffled = append(shuffled, sequence[rng.IntN(len(sequence))])
		}
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })

		var got State
		for _, e := range shuffled {
			got, _ = Apply(got, e)
		}
		if got.Status != want.Status || *got.Driver != *want.Driver || !got.PositionAt.Equal(want.PositionAt) {
			t.Fatalf("rodada %d: estado %+v, quero %+v", i, got, want)
		}
	}
}
