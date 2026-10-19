package geo

import (
	"math"
	"testing"
)

var (
	se       = Point{Lat: -23.5505, Lng: -46.6333} // Praça da Sé, São Paulo
	paulista = Point{Lat: -23.5614, Lng: -46.6559} // MASP, Av. Paulista
)

func TestDistanceKm(t *testing.T) {
	tests := []struct {
		name string
		a, b Point
		want float64 // km
		tol  float64 // tolerância em km
	}{
		{"mesmo ponto", se, se, 0, 1e-9},
		// 1 grau de longitude no equador = 2*pi*R/360
		{"1 grau no equador", Point{0, 0}, Point{0, 1}, 2 * math.Pi * EarthRadiusKm / 360, 1e-6},
		// de um polo ao outro = meia volta = pi*R
		{"polo a polo", Point{90, 0}, Point{-90, 0}, math.Pi * EarthRadiusKm, 1e-6},
		// pontos opostos no globo (antípodas) também dão pi*R
		{"antípodas", Point{0, 0}, Point{0, 180}, math.Pi * EarthRadiusKm, 1e-6},
		// Sé -> MASP: ~2,6 km em linha reta
		{"Sé até o MASP", se, paulista, 2.6, 0.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DistanceKm(tt.a, tt.b)
			if math.Abs(got-tt.want) > tt.tol {
				t.Errorf("DistanceKm = %.6f km, quero %.6f ± %.6f", got, tt.want, tt.tol)
			}
			// a distância de a até b é a mesma de b até a
			if back := DistanceKm(tt.b, tt.a); math.Abs(back-got) > 1e-9 {
				t.Errorf("não é simétrica: %.9f x %.9f", got, back)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		p       Point
		wantErr bool
	}{
		{"São Paulo", se, false},
		{"limites", Point{90, -180}, false},
		{"latitude alta", Point{91, 0}, true},
		{"longitude baixa", Point{0, -181}, true},
		{"NaN", Point{math.NaN(), 0}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.p.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() = %v, quero erro? %v", err, tt.wantErr)
			}
		})
	}
}

func TestInterpolate(t *testing.T) {
	a, b := Point{0, 0}, Point{10, 20}
	tests := []struct {
		f    float64
		want Point
	}{
		{0, a},
		{0.5, Point{5, 10}},
		{1, b},
		{-1, a}, // fora de [0,1] é cortado
		{2, b},
	}
	for _, tt := range tests {
		if got := Interpolate(a, b, tt.f); got != tt.want {
			t.Errorf("Interpolate(f=%v) = %v, quero %v", tt.f, got, tt.want)
		}
	}
}
