// Package eta calcula a previsão de chegada da entrega ao comprador.
//
// A conta é simples de propósito, para ser fácil de explicar e de testar:
//
//	distância restante (linha reta) x fator de rota  /  velocidade média
//
// O fator de rota corrige o fato de que a rua não é uma linha reta. Os dois
// números são PARÂMETROS chutados e configuráveis, não medidos. Em produção eles
// viriam do histórico de entregas (por cidade, horário e tipo de veículo), ou de
// uma API de rotas.
package eta

import (
	"math"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

// Params são as suposições do cálculo.
type Params struct {
	SpeedKmh    float64 // velocidade média do motorista
	RouteFactor float64 // rota pelas ruas / linha reta (>= 1)
}

// DefaultParams: 20 km/h e fator 1,3. São suposições razoáveis para moto em
// cidade grande com trânsito, ajustáveis por ETA_SPEED_KMH e ETA_ROUTE_FACTOR.
var DefaultParams = Params{SpeedKmh: 20, RouteFactor: 1.3}

// Input é o que se sabe do pedido no momento.
type Input struct {
	Status     order.Status
	Driver     *geo.Point // última posição conhecida do motorista (nil = desconhecida)
	PositionAt time.Time  // quando essa posição foi informada
	Pickup     geo.Point
	Dropoff    geo.Point
}

// Estimate é a previsão.
type Estimate struct {
	DistanceKm float64       // distância restante estimada pelas ruas
	Duration   time.Duration // tempo restante a partir de PositionAt
	Arrival    time.Time     // horário previsto de chegada ao comprador
}

// Calculate devolve a previsão. ok = false quando não dá para prever: pedido
// sem motorista, posição desconhecida ou entrega já terminada.
func Calculate(in Input, p Params) (est Estimate, ok bool) {
	if in.Driver == nil || in.PositionAt.IsZero() || !in.Status.Active() || p.SpeedKmh <= 0 {
		return Estimate{}, false
	}
	factor := math.Max(p.RouteFactor, 1)

	var straight float64
	if in.Status == order.StatusAssigned {
		// ainda vai buscar o pacote: motorista -> coleta -> comprador
		straight = geo.DistanceKm(*in.Driver, in.Pickup) + geo.DistanceKm(in.Pickup, in.Dropoff)
	} else {
		// já está com o pacote: motorista -> comprador
		straight = geo.DistanceKm(*in.Driver, in.Dropoff)
	}

	km := straight * factor
	dur := time.Duration(km / p.SpeedKmh * float64(time.Hour)).Round(time.Second)
	return Estimate{DistanceKm: km, Duration: dur, Arrival: in.PositionAt.Add(dur)}, true
}

// MinutesLeft devolve os minutos que faltam a partir de now, arredondando para
// cima (falta 1 min e 10 s => "2 min"). Nunca é negativo: se a previsão passou,
// o motorista está chegando (0).
func (e Estimate) MinutesLeft(now time.Time) int {
	left := e.Arrival.Sub(now)
	if left <= 0 {
		return 0
	}
	return int(math.Ceil(left.Minutes()))
}
