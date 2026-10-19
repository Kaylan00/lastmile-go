// Package tracking é o núcleo do tracking-service: aplica os eventos da
// entrega ao estado do pedido e monta o que o comprador vê (status + ETA).
package tracking

import (
	"time"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

// State é o que o tracking sabe sobre um pedido.
type State struct {
	OrderID      string
	Status       order.Status
	StatusAt     time.Time // quando aconteceu o evento que definiu o status atual
	DriverID     string
	Pickup       geo.Point
	Dropoff      geo.Point
	Driver       *geo.Point // última posição conhecida do motorista
	PositionAt   time.Time  // quando essa posição foi informada
	BuyerMessage string     // mensagem para o comprador quando a tentativa de entrega falha
}

// Apply aplica um evento ao estado e diz se alguma coisa mudou.
//
// É uma função pura: não acessa banco nem rede, só recebe e devolve valores.
// Por isso dá para testar todas as combinações com uma tabela, e o mesmo
// código roda no serviço (com Postgres) e na demo (em memória).
//
// Regras para eventos duplicados ou fora de ordem:
//   - status: só avança (order.Supersedes). Evento atrasado não faz o status voltar.
//   - posição: só troca se o evento for mais novo que a posição atual. Uma
//     posição antiga que chega atrasada não "teletransporta" o motorista para trás.
func Apply(s State, e events.Event) (State, bool) {
	changed := false

	if s.OrderID == "" {
		// Primeiro evento deste pedido. Pode não ser o order.assigned (o Pub/Sub
		// não garante ordem), por isso todo evento traz coleta e entrega.
		s = State{OrderID: e.OrderID, Status: order.StatusPending, Pickup: e.Pickup, Dropoff: e.Dropoff}
		changed = true
	}

	if st, ok := e.Type.Status(); ok && order.Supersedes(s.Status, st) {
		s.Status = st
		s.StatusAt = e.OccurredAt
		changed = true
	}

	if s.DriverID == "" && e.DriverID != "" {
		s.DriverID = e.DriverID
		changed = true
	}

	if e.Position != nil && e.OccurredAt.After(s.PositionAt) {
		pos := *e.Position // cópia: o estado não aponta para dentro do evento
		s.Driver = &pos
		s.PositionAt = e.OccurredAt
		changed = true
	}

	return s, changed
}
