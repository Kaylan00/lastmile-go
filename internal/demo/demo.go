// Package demo é a versão de demonstração do rastreio, usada na demo pública
// (função serverless na Vercel) e em `go run ./cmd/demo`.
//
// O que é de verdade aqui: a escolha do motorista mais próximo (geo), a
// sequência de eventos, a aplicação de cada evento ao estado (tracking.Apply),
// a previsão de chegada (eta) e a mensagem padrão do notify. É o mesmo código
// do tracking-service.
//
// O que NÃO tem aqui: Postgres, Pub/Sub e os dois serviços separados. Uma
// função serverless não guarda estado entre chamadas, então o pedido de demo
// carrega tudo no próprio id (hora de criação, coleta, entrega e uma semente),
// e a entrega é simulada em função do tempo: a cada consulta, os eventos que
// "já aconteceram" são gerados de novo e aplicados ao estado.
package demo

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/eta"
	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
	"github.com/Kaylan00/lastmile-go/internal/tracking"
)

// Parâmetros da simulação. A velocidade é alta de propósito: a entrega inteira
// cabe em 2 ou 3 minutos, que é o tempo que alguém espera olhando uma demo.
const (
	SpeedKmh     = 90.0
	MaxTripKm    = 5.0
	assignAfter  = 3 * time.Second // tempo até "alocar" o motorista
	loadingTime  = 5 * time.Second // tempo parado na coleta
	locationTick = 4 * time.Second // de quanto em quanto tempo o motorista manda a posição
	maxAge       = 24 * time.Hour  // pedidos de demo expiram depois disso
	idPrefix     = "demo-"
	failureNote  = "cliente ausente"
)

// Params do ETA na demo: a mesma velocidade da simulação e fator 1, porque o
// motorista simulado anda em linha reta.
var Params = eta.Params{SpeedKmh: SpeedKmh, RouteFactor: 1}

// Center é o ponto de referência da demo (Av. Paulista, São Paulo).
var Center = geo.Point{Lat: -23.5614, Lng: -46.6559}

// Order é um pedido de demonstração.
type Order struct {
	CreatedAt time.Time
	Pickup    geo.Point
	Dropoff   geo.Point
	Seed      uint32
}

// NewOrder valida e cria um pedido de demonstração.
func NewOrder(now time.Time, pickup, dropoff geo.Point, seed uint32) (Order, error) {
	if err := pickup.Validate(); err != nil {
		return Order{}, fmt.Errorf("pickup: %w", err)
	}
	if err := dropoff.Validate(); err != nil {
		return Order{}, fmt.Errorf("dropoff: %w", err)
	}
	if d := geo.DistanceKm(pickup, dropoff); d > MaxTripKm {
		return Order{}, fmt.Errorf("na demo a entrega vai até %.0f km (pedido com %.1f km)", MaxTripKm, d)
	}
	o := Order{CreatedAt: now.UTC().Truncate(time.Millisecond), Pickup: pickup, Dropoff: dropoff, Seed: seed}
	// o id guarda as coordenadas em float32; usamos já os valores arredondados
	return ParseID(o.ID())
}

// RandomOrder cria um pedido com coleta perto do centro e entrega a 1,5-3 km.
func RandomOrder(now time.Time, rng *rand.Rand) Order {
	pickup := offset(Center, 0.3+rng.Float64()*1.5, rng.Float64()*360)
	dropoff := offset(pickup, 1.5+rng.Float64()*1.5, rng.Float64()*360)
	o, _ := NewOrder(now, pickup, dropoff, rng.Uint32())
	return o
}

// ID codifica o pedido: 8 bytes de hora (ms) + 4 coordenadas float32 + 4 bytes
// de semente = 28 bytes, em base64 para caber numa URL.
func (o Order) ID() string {
	b := make([]byte, 28)
	binary.BigEndian.PutUint64(b[0:], uint64(o.CreatedAt.UnixMilli()))
	for i, f := range []float64{o.Pickup.Lat, o.Pickup.Lng, o.Dropoff.Lat, o.Dropoff.Lng} {
		binary.BigEndian.PutUint32(b[8+4*i:], math.Float32bits(float32(f)))
	}
	binary.BigEndian.PutUint32(b[24:], o.Seed)
	return idPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// ErrInvalidID é devolvido para ids que não são de demo ou estão estragados.
var ErrInvalidID = errors.New("id de pedido de demonstração inválido")

// ParseID faz o caminho inverso de ID.
func ParseID(id string) (Order, error) {
	raw, ok := strings.CutPrefix(id, idPrefix)
	if !ok {
		return Order{}, ErrInvalidID
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != 28 {
		return Order{}, ErrInvalidID
	}
	f := func(i int) float64 { return float64(math.Float32frombits(binary.BigEndian.Uint32(b[8+4*i:]))) }
	o := Order{
		CreatedAt: time.UnixMilli(int64(binary.BigEndian.Uint64(b[0:]))).UTC(),
		Pickup:    geo.Point{Lat: f(0), Lng: f(1)},
		Dropoff:   geo.Point{Lat: f(2), Lng: f(3)},
		Seed:      binary.BigEndian.Uint32(b[24:]),
	}
	if o.Pickup.Validate() != nil || o.Dropoff.Validate() != nil || geo.DistanceKm(o.Pickup, o.Dropoff) > MaxTripKm+0.01 {
		return Order{}, ErrInvalidID
	}
	return o, nil
}

// orderID é um UUID derivado do id de demo (o contrato de eventos exige UUID).
func (o Order) orderID() string { return uuidFrom(o.ID()) }

// Expired diz se o pedido é velho demais (ou do futuro).
func (o Order) Expired(now time.Time) bool {
	age := now.Sub(o.CreatedAt)
	return age > maxAge || age < -time.Minute
}

type driver struct {
	ID  string
	Pos geo.Point
}

// fleet são os motoristas livres perto da coleta, sorteados a partir da semente.
func (o Order) fleet() []driver {
	rng := rand.New(rand.NewPCG(uint64(o.Seed), 42))
	drivers := make([]driver, 6)
	for i := range drivers {
		drivers[i] = driver{
			ID:  fmt.Sprintf("moto-%02d", rng.IntN(90)+10),
			Pos: offset(o.Pickup, 0.6+rng.Float64()*1.4, rng.Float64()*360),
		}
	}
	return drivers
}

// nearest escolhe o motorista mais próximo da coleta (o mesmo critério do
// dispatch, que lá é feito em SQL com haversine_km).
func nearest(drivers []driver, p geo.Point) driver {
	best := drivers[0]
	for _, d := range drivers[1:] {
		if geo.DistanceKm(d.Pos, p) < geo.DistanceKm(best.Pos, p) {
			best = d
		}
	}
	return best
}

// Fails diz se esta entrega termina em tentativa falha (1 em cada 5 sementes),
// para a demo também mostrar a mensagem ao comprador.
func (o Order) Fails() bool { return o.Seed%5 == 0 }

// travel devolve quanto tempo leva para andar km na velocidade da demo.
func travel(km float64) time.Duration {
	return time.Duration(km / SpeedKmh * float64(time.Hour))
}

// Events gera os eventos que já aconteceram até now, em ordem.
func (o Order) Events(now time.Time) []events.Event {
	drv := nearest(o.fleet(), o.Pickup)
	toPickup := travel(geo.DistanceKm(drv.Pos, o.Pickup))
	toDropoff := travel(geo.DistanceKm(o.Pickup, o.Dropoff))

	tAssigned := o.CreatedAt.Add(assignAfter)
	tPicked := tAssigned.Add(toPickup)
	tOut := tPicked.Add(loadingTime)
	tEnd := tOut.Add(toDropoff)

	created := events.Event{
		ID: uuidFrom(o.ID() + "/created"), Type: events.OrderCreated, OrderID: o.orderID(),
		OccurredAt: o.CreatedAt, Pickup: o.Pickup, Dropoff: o.Dropoff,
	}
	out := []events.Event{created}
	add := func(typ events.Type, at time.Time, pos geo.Point, note string) {
		if at.After(now) {
			return
		}
		p := pos
		out = append(out, events.Event{
			ID: uuidFrom(fmt.Sprintf("%s/%d", o.ID(), len(out))), Type: typ, OrderID: o.orderID(),
			DriverID: drv.ID, OccurredAt: at, Position: &p, Pickup: o.Pickup, Dropoff: o.Dropoff, Note: note,
		})
	}

	add(events.OrderAssigned, tAssigned, drv.Pos, "")
	for t := tAssigned.Add(locationTick); t.Before(tPicked); t = t.Add(locationTick) {
		f := float64(t.Sub(tAssigned)) / float64(toPickup)
		add(events.OrderLocation, t, geo.Interpolate(drv.Pos, o.Pickup, f), "")
	}
	add(events.OrderPickedUp, tPicked, o.Pickup, "")
	add(events.OrderOutForDelivery, tOut, o.Pickup, "")
	for t := tOut.Add(locationTick); t.Before(tEnd); t = t.Add(locationTick) {
		f := float64(t.Sub(tOut)) / float64(toDropoff)
		add(events.OrderLocation, t, geo.Interpolate(o.Pickup, o.Dropoff, f), "")
	}
	if o.Fails() {
		add(events.OrderFailedAttempt, tEnd, o.Dropoff, failureNote)
	} else {
		add(events.OrderDelivered, tEnd, o.Dropoff, "")
	}
	return out
}

// View monta a resposta de rastreio em now, aplicando os eventos com o mesmo
// tracking.Apply e o mesmo tracking.BuildView do tracking-service.
func (o Order) View(now time.Time) tracking.View {
	state := tracking.State{OrderID: o.orderID(), Status: order.StatusPending, Pickup: o.Pickup, Dropoff: o.Dropoff}
	var timeline []tracking.TimelineEntry
	for _, e := range o.Events(now) {
		state, _ = tracking.Apply(state, e)
		if e.Type != events.OrderLocation {
			timeline = append(timeline, tracking.TimelineEntry{Type: e.Type, At: e.OccurredAt})
		}
	}
	v := tracking.BuildView(state, timeline, Params, now)
	v.OrderID = o.ID()
	v.Demo = true
	return v
}

// offset devolve o ponto a km quilômetros de p, na direção bearing (graus).
// Aproximação plana, boa para distâncias pequenas dentro de uma cidade.
func offset(p geo.Point, km, bearing float64) geo.Point {
	rad := bearing * math.Pi / 180
	dLat := km * math.Cos(rad) / 111.32
	dLng := km * math.Sin(rad) / (111.32 * math.Cos(p.Lat*math.Pi/180))
	return geo.Point{Lat: p.Lat + dLat, Lng: p.Lng + dLng}
}

// uuidFrom gera um UUID estável (sempre o mesmo para a mesma entrada).
func uuidFrom(s string) string {
	h := sha256.Sum256([]byte(s))
	h[6] = (h[6] & 0x0f) | 0x40
	h[8] = (h[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}
