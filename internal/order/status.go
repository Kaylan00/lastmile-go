// Package order define os status de um pedido e as regras de mudança de status.
// Os dois serviços usam este pacote, então as regras são as mesmas nos dois.
//
//	pending -> assigned -> picked_up -> out_for_delivery -> delivered
//	                                                    \-> failed_attempt
package order

// Status é a etapa em que o pedido está.
type Status string

const (
	StatusPending        Status = "pending"          // criado, esperando motorista
	StatusAssigned       Status = "assigned"         // motorista alocado, indo buscar
	StatusPickedUp       Status = "picked_up"        // motorista coletou o pacote
	StatusOutForDelivery Status = "out_for_delivery" // a caminho do comprador
	StatusDelivered      Status = "delivered"        // entregue (fim)
	StatusFailedAttempt  Status = "failed_attempt"   // tentativa falhou, ex.: cliente ausente (fim)
)

// rank é a posição de cada status na linha do tempo da entrega. Os dois finais
// têm o mesmo rank: um pedido termina entregue OU com tentativa falha.
var rank = map[Status]int{
	StatusPending:        0,
	StatusAssigned:       1,
	StatusPickedUp:       2,
	StatusOutForDelivery: 3,
	StatusDelivered:      4,
	StatusFailedAttempt:  4,
}

// next lista as transições permitidas quando o motorista informa o andamento.
var next = map[Status][]Status{
	StatusPending:        {StatusAssigned},
	StatusAssigned:       {StatusPickedUp},
	StatusPickedUp:       {StatusOutForDelivery},
	StatusOutForDelivery: {StatusDelivered, StatusFailedAttempt},
}

// Valid diz se s é um status conhecido.
func (s Status) Valid() bool {
	_, ok := rank[s]
	return ok
}

// Terminal diz se o pedido já acabou (entregue ou tentativa falha).
func (s Status) Terminal() bool {
	return s == StatusDelivered || s == StatusFailedAttempt
}

// Active diz se o pedido está com um motorista (alocado e ainda não terminado).
func (s Status) Active() bool {
	return s == StatusAssigned || s == StatusPickedUp || s == StatusOutForDelivery
}

// CanTransition é a regra estrita, usada pelo dispatch quando o motorista
// informa uma etapa: só vale andar um passo por vez, na ordem.
func CanTransition(from, to Status) bool {
	for _, s := range next[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Supersedes é a regra usada pelo tracking, que pode receber eventos fora de
// ordem: o status novo só substitui o atual se estiver MAIS ADIANTE na linha do
// tempo e o atual ainda não for final. Assim o status nunca "volta".
//
// Exemplo: chegou "delivered" e depois, atrasado, "picked_up": o "picked_up" é
// ignorado. Se chegar "out_for_delivery" antes de "picked_up", ele é aceito
// (pula uma etapa) e o "picked_up" que vier depois é ignorado.
func Supersedes(current, incoming Status) bool {
	if !incoming.Valid() || current.Terminal() {
		return false
	}
	return rank[incoming] > rank[current]
}
