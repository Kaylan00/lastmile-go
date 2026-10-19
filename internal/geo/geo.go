// Package geo calcula distâncias entre coordenadas (latitude/longitude).
package geo

import (
	"errors"
	"fmt"
	"math"
)

// EarthRadiusKm é o raio médio da Terra. A mesma constante está na função
// haversine_km do banco (migrations/dispatch), para os dois darem o mesmo valor.
const EarthRadiusKm = 6371.0

// Point é uma coordenada em graus decimais.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Validate confere se a coordenada existe no planeta.
func (p Point) Validate() error {
	switch {
	case math.IsNaN(p.Lat) || math.IsNaN(p.Lng):
		return errors.New("coordenada não é um número")
	case p.Lat < -90 || p.Lat > 90:
		return fmt.Errorf("latitude %v fora de [-90, 90]", p.Lat)
	case p.Lng < -180 || p.Lng > 180:
		return fmt.Errorf("longitude %v fora de [-180, 180]", p.Lng)
	}
	return nil
}

// DistanceKm devolve a distância em linha reta, sobre a superfície da Terra,
// entre a e b (fórmula de Haversine). Não é a distância pelas ruas.
func DistanceKm(a, b Point) float64 {
	lat1, lat2 := radians(a.Lat), radians(b.Lat)
	dLat := radians(b.Lat - a.Lat)
	dLng := radians(b.Lng - a.Lng)

	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(lat1)*math.Cos(lat2)*math.Pow(math.Sin(dLng/2), 2)
	// Min(1, ...) protege contra arredondamento que deixaria a raiz um pouco acima de 1.
	return 2 * EarthRadiusKm * math.Asin(math.Min(1, math.Sqrt(h)))
}

// Interpolate devolve o ponto a uma fração f (0 a 1) do caminho de a até b.
// É uma interpolação linear: boa para distâncias curtas (dentro de uma cidade).
func Interpolate(a, b Point, f float64) Point {
	f = math.Max(0, math.Min(1, f))
	return Point{
		Lat: a.Lat + (b.Lat-a.Lat)*f,
		Lng: a.Lng + (b.Lng-a.Lng)*f,
	}
}

func radians(deg float64) float64 { return deg * math.Pi / 180 }
