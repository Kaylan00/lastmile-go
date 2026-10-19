// Package dispatch é o núcleo do dispatch-service: pedidos, motoristas e a
// alocação de cada pedido ao motorista livre mais próximo.
package dispatch

import (
	"errors"
	"regexp"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

// Erros que o HTTP traduz em status (404, 409...). São variáveis "sentinela":
// quem recebe compara com errors.Is(err, dispatch.ErrNotFound).
var (
	ErrNotFound          = errors.New("não encontrado")
	ErrDriverBusy        = errors.New("motorista está com um pedido em andamento")
	ErrNoPendingOrder    = errors.New("nenhum pedido pendente")
	ErrNoDriverAvailable = errors.New("nenhum motorista livre")
	ErrInvalidTransition = errors.New("mudança de status inválida")
	ErrWrongDriver       = errors.New("o pedido não está com este motorista")
)

// DriverStatus é a situação do motorista.
type DriverStatus string

const (
	DriverOffline   DriverStatus = "offline"   // fora do turno
	DriverAvailable DriverStatus = "available" // livre, esperando pedido
	DriverBusy      DriverStatus = "busy"      // com um pedido em andamento
)

// Order é um pedido de entrega: buscar em Pickup e entregar em Dropoff.
type Order struct {
	ID         string       `json:"id"`
	Status     order.Status `json:"status"`
	Pickup     geo.Point    `json:"pickup"`
	Dropoff    geo.Point    `json:"dropoff"`
	DriverID   string       `json:"driver_id,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	AssignedAt *time.Time   `json:"assigned_at,omitempty"`
}

// Driver é um motorista e a última posição conhecida dele.
type Driver struct {
	ID             string       `json:"id"`
	Status         DriverStatus `json:"status"`
	Position       *geo.Point   `json:"position,omitempty"`
	CurrentOrderID string       `json:"current_order_id,omitempty"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

var (
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	driverIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// validOrderID evita mandar ao banco um id que nem é UUID (o Postgres daria erro de sintaxe).
func validOrderID(id string) bool { return uuidRe.MatchString(id) }

// validDriverID aceita letras, números, "-" e "_" (ex.: "drv-01").
func validDriverID(id string) bool { return driverIDRe.MatchString(id) }
