// Package events é o contrato entre os serviços: o formato dos eventos que o
// dispatch publica no Pub/Sub e o tracking consome.
//
// Mudar um campo aqui muda o contrato. Por isso só se ACRESCENTA campo opcional;
// renomear ou tirar campo quebraria o serviço que ainda roda a versão antiga.
package events

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Type é o tipo do evento.
type Type string

const (
	OrderCreated        Type = "order.created" // pedido criado, ainda procurando motorista
	OrderAssigned       Type = "order.assigned"
	OrderPickedUp       Type = "order.picked_up"
	OrderOutForDelivery Type = "order.out_for_delivery"
	OrderDelivered      Type = "order.delivered"
	OrderFailedAttempt  Type = "order.failed_attempt"
	OrderLocation       Type = "order.location_updated" // posição nova do motorista, sem mudar status
)

var statusByType = map[Type]order.Status{
	OrderCreated:        order.StatusPending,
	OrderAssigned:       order.StatusAssigned,
	OrderPickedUp:       order.StatusPickedUp,
	OrderOutForDelivery: order.StatusOutForDelivery,
	OrderDelivered:      order.StatusDelivered,
	OrderFailedAttempt:  order.StatusFailedAttempt,
}

// Status devolve o status do pedido que o evento representa.
// O evento de posição não representa status (ok = false).
func (t Type) Status() (s order.Status, ok bool) {
	s, ok = statusByType[t]
	return s, ok
}

// Valid diz se o tipo é conhecido.
func (t Type) Valid() bool {
	_, ok := statusByType[t]
	return ok || t == OrderLocation
}

// TypeFor devolve o tipo de evento de uma mudança de status.
func TypeFor(s order.Status) (Type, bool) {
	for t, st := range statusByType {
		if st == s {
			return t, true
		}
	}
	return "", false
}

// Event é uma coisa que aconteceu com um pedido.
//
// Cada evento carrega tudo o que o tracking precisa (coleta, entrega e posição
// do motorista). Assim, se um evento chegar antes do "order.assigned" (o Pub/Sub
// não garante ordem), o tracking consegue criar o registro mesmo assim.
type Event struct {
	ID         string     `json:"event_id"` // único: é a chave da idempotência no consumidor
	Type       Type       `json:"type"`
	OrderID    string     `json:"order_id"`
	DriverID   string     `json:"driver_id"`
	OccurredAt time.Time  `json:"occurred_at"`
	Position   *geo.Point `json:"position,omitempty"` // onde o motorista estava
	Pickup     geo.Point  `json:"pickup"`
	Dropoff    geo.Point  `json:"dropoff"`
	Note       string     `json:"note,omitempty"` // ocorrência escrita pelo motorista (ex.: "cliente ausente")
}

// Validate confere os campos obrigatórios. Evento inválido não adianta
// reprocessar: o consumidor manda direto para a dead-letter.
func (e Event) Validate() error {
	var errs []error
	if !uuidRe.MatchString(e.ID) {
		errs = append(errs, fmt.Errorf("event_id %q não é UUID", e.ID))
	}
	if !e.Type.Valid() {
		errs = append(errs, fmt.Errorf("tipo desconhecido %q", e.Type))
	}
	if !uuidRe.MatchString(e.OrderID) {
		errs = append(errs, fmt.Errorf("order_id %q não é UUID", e.OrderID))
	}
	if e.OccurredAt.IsZero() {
		errs = append(errs, errors.New("occurred_at vazio"))
	}
	if err := e.Pickup.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("pickup: %w", err))
	}
	if err := e.Dropoff.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("dropoff: %w", err))
	}
	if e.Position != nil {
		if err := e.Position.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("position: %w", err))
		}
	} else if e.Type == OrderLocation {
		errs = append(errs, errors.New("order.location_updated sem position"))
	}
	return errors.Join(errs...)
}

// Parse lê um evento em JSON e valida.
func Parse(data []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		return Event{}, fmt.Errorf("JSON inválido: %w", err)
	}
	if err := e.Validate(); err != nil {
		return Event{}, err
	}
	return e, nil
}

// NewID gera um UUID versão 4 (aleatório) com crypto/rand.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])      // crypto/rand.Read nunca devolve erro (Go 1.24+)
	b[6] = (b[6] & 0x0f) | 0x40 // versão 4
	b[8] = (b[8] & 0x3f) | 0x80 // variante RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Now devolve a hora atual em UTC com precisão de microssegundo, a mesma do
// timestamptz do Postgres. Assim comparar horários do Go e do banco não engana.
func Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
