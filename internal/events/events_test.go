package events

import (
	"regexp"
	"testing"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

func validEvent() Event {
	return Event{
		ID:         NewID(),
		Type:       OrderPickedUp,
		OrderID:    "7f1c0e7e-1f4b-4b8e-9d55-3f6f2a8f8a10",
		DriverID:   "drv-01",
		OccurredAt: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
		Position:   &geo.Point{Lat: -23.55, Lng: -46.63},
		Pickup:     geo.Point{Lat: -23.55, Lng: -46.63},
		Dropoff:    geo.Point{Lat: -23.56, Lng: -46.65},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		change  func(e *Event)
		wantErr bool
	}{
		{"válido", func(*Event) {}, false},
		{"sem id", func(e *Event) { e.ID = "" }, true},
		{"id que não é UUID", func(e *Event) { e.ID = "evento-1" }, true},
		{"tipo desconhecido", func(e *Event) { e.Type = "order.teleported" }, true},
		{"sem pedido", func(e *Event) { e.OrderID = "" }, true},
		{"sem horário", func(e *Event) { e.OccurredAt = time.Time{} }, true},
		{"coleta inválida", func(e *Event) { e.Pickup.Lat = 200 }, true},
		{"posição sem coordenada válida", func(e *Event) { e.Position = &geo.Point{Lat: 0, Lng: 999} }, true},
		{"posição é opcional em mudança de status", func(e *Event) { e.Position = nil }, false},
		{"posição é obrigatória no evento de localização", func(e *Event) { e.Type, e.Position = OrderLocation, nil }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEvent()
			tt.change(&e)
			if err := e.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() = %v, quero erro? %v", err, tt.wantErr)
			}
		})
	}
}

func TestParse(t *testing.T) {
	if _, err := Parse([]byte(`{não é json`)); err == nil {
		t.Error("esperava erro com JSON quebrado")
	}
	if _, err := Parse([]byte(`{"event_id": "x"}`)); err == nil {
		t.Error("esperava erro com evento incompleto")
	}
	ok := `{"event_id":"3b8f6a52-6c1e-4d0b-9a3e-2f1c7d9e0a11","type":"order.delivered",
		"order_id":"7f1c0e7e-1f4b-4b8e-9d55-3f6f2a8f8a10","driver_id":"drv-01",
		"occurred_at":"2026-10-19T12:00:00Z","pickup":{"lat":-23.5,"lng":-46.6},"dropoff":{"lat":-23.6,"lng":-46.7}}`
	e, err := Parse([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if e.Type != OrderDelivered || e.DriverID != "drv-01" {
		t.Errorf("evento lido errado: %+v", e)
	}
}

func TestTypeStatusMapping(t *testing.T) {
	for _, s := range []order.Status{order.StatusPending, order.StatusAssigned, order.StatusPickedUp, order.StatusOutForDelivery, order.StatusDelivered, order.StatusFailedAttempt} {
		typ, ok := TypeFor(s)
		if !ok {
			t.Fatalf("status %s sem tipo de evento", s)
		}
		if back, ok := typ.Status(); !ok || back != s {
			t.Errorf("%s -> %s -> %s", s, typ, back)
		}
	}
	if _, ok := OrderLocation.Status(); ok {
		t.Error("evento de posição não muda status")
	}
}

func TestNewID(t *testing.T) {
	uuidV4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 1000 {
		id := NewID()
		if !uuidV4.MatchString(id) {
			t.Fatalf("%q não é UUID v4", id)
		}
		if seen[id] {
			t.Fatalf("id repetido: %s", id)
		}
		seen[id] = true
	}
}
