package eta

import (
	"math"
	"testing"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

// Pontos sobre o equador, a cada 0,1 grau de longitude (~11,12 km), para as
// contas serem fáceis de conferir de cabeça.
var (
	a = geo.Point{Lat: 0, Lng: 0}
	b = geo.Point{Lat: 0, Lng: 0.1}
	c = geo.Point{Lat: 0, Lng: 0.2}
)

func TestCalculate(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	seg := geo.DistanceKm(a, b) // ~11,12 km
	p := Params{SpeedKmh: 20, RouteFactor: 1}

	tests := []struct {
		name   string
		in     Input
		params Params
		wantOK bool
		wantKm float64
	}{
		{
			name:   "alocado: motorista -> coleta -> comprador",
			in:     Input{Status: order.StatusAssigned, Driver: &a, PositionAt: t0, Pickup: b, Dropoff: c},
			params: p, wantOK: true, wantKm: 2 * seg,
		},
		{
			name:   "coletado: só motorista -> comprador",
			in:     Input{Status: order.StatusPickedUp, Driver: &b, PositionAt: t0, Pickup: a, Dropoff: c},
			params: p, wantOK: true, wantKm: seg,
		},
		{
			name:   "saiu para entrega",
			in:     Input{Status: order.StatusOutForDelivery, Driver: &a, PositionAt: t0, Pickup: a, Dropoff: c},
			params: p, wantOK: true, wantKm: 2 * seg,
		},
		{
			name:   "fator de rota aumenta a distância",
			in:     Input{Status: order.StatusOutForDelivery, Driver: &b, PositionAt: t0, Dropoff: c},
			params: Params{SpeedKmh: 20, RouteFactor: 1.5}, wantOK: true, wantKm: 1.5 * seg,
		},
		{
			name:   "fator menor que 1 é tratado como 1",
			in:     Input{Status: order.StatusOutForDelivery, Driver: &b, PositionAt: t0, Dropoff: c},
			params: Params{SpeedKmh: 20, RouteFactor: 0.5}, wantOK: true, wantKm: seg,
		},
		{"pendente não tem previsão", Input{Status: order.StatusPending, Driver: &a, PositionAt: t0}, p, false, 0},
		{"entregue não tem previsão", Input{Status: order.StatusDelivered, Driver: &a, PositionAt: t0}, p, false, 0},
		{"tentativa falha não tem previsão", Input{Status: order.StatusFailedAttempt, Driver: &a, PositionAt: t0}, p, false, 0},
		{"sem posição do motorista", Input{Status: order.StatusPickedUp, PositionAt: t0}, p, false, 0},
		{"velocidade zero", Input{Status: order.StatusPickedUp, Driver: &a, PositionAt: t0}, Params{}, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Calculate(tt.in, tt.params)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, quero %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if math.Abs(got.DistanceKm-tt.wantKm) > 1e-9 {
				t.Errorf("distância = %.4f km, quero %.4f", got.DistanceKm, tt.wantKm)
			}
			// tempo = distância / velocidade
			wantDur := time.Duration(tt.wantKm / tt.params.SpeedKmh * float64(time.Hour)).Round(time.Second)
			if got.Duration != wantDur {
				t.Errorf("duração = %v, quero %v", got.Duration, wantDur)
			}
			if !got.Arrival.Equal(t0.Add(wantDur)) {
				t.Errorf("chegada = %v, quero %v", got.Arrival, t0.Add(wantDur))
			}
		})
	}
}

func TestMinutesLeft(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	e := Estimate{Arrival: t0.Add(10 * time.Minute)}
	tests := []struct {
		now  time.Time
		want int
	}{
		{t0, 10},
		{t0.Add(8*time.Minute + 50*time.Second), 2}, // falta 1 min 10 s: arredonda para cima
		{t0.Add(10 * time.Minute), 0},
		{t0.Add(15 * time.Minute), 0}, // previsão passou: nunca negativo
	}
	for _, tt := range tests {
		if got := e.MinutesLeft(tt.now); got != tt.want {
			t.Errorf("MinutesLeft(%v) = %d, quero %d", tt.now.Sub(t0), got, tt.want)
		}
	}
}
