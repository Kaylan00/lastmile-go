package demo

import (
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/notify"
	"github.com/Kaylan00/lastmile-go/internal/order"
	"github.com/Kaylan00/lastmile-go/internal/tracking"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func TestIDRoundTrip(t *testing.T) {
	o, err := NewOrder(t0, geo.Point{Lat: -23.55, Lng: -46.63}, geo.Point{Lat: -23.56, Lng: -46.65}, 12345)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseID(o.ID())
	if err != nil {
		t.Fatal(err)
	}
	if back != o {
		t.Fatalf("ida e volta mudou o pedido: %+v x %+v", back, o)
	}
	if !strings.HasPrefix(o.ID(), "demo-") || len(o.ID()) > 50 {
		t.Fatalf("id estranho: %s", o.ID())
	}
}

func TestParseIDRejectsGarbage(t *testing.T) {
	for _, id := range []string{"", "demo-", "demo-!!!", "7f1c0e7e-1f4b-4b8e-9d55-3f6f2a8f8a10", "demo-" + strings.Repeat("A", 37)} {
		if _, err := ParseID(id); err == nil {
			t.Errorf("ParseID(%q) deveria falhar", id)
		}
	}
}

func TestNewOrderRejectsLongTrip(t *testing.T) {
	_, err := NewOrder(t0, geo.Point{Lat: -23.55, Lng: -46.63}, geo.Point{Lat: -22.90, Lng: -43.17}, 1) // SP -> Rio
	if err == nil {
		t.Fatal("entrega de 350 km não deveria ser aceita na demo")
	}
}

// A entrega simulada passa por todas as etapas, o status nunca volta e o ETA
// vai diminuindo até a chegada.
func TestDeliveryOverTime(t *testing.T) {
	for _, seed := range []uint32{1, 5} { // 5 % 5 == 0: tentativa falha
		o, err := NewOrder(t0, geo.Point{Lat: -23.5505, Lng: -46.6333}, geo.Point{Lat: -23.5614, Lng: -46.6559}, seed)
		if err != nil {
			t.Fatal(err)
		}

		if v := o.View(t0); v.Status != order.StatusPending || v.ETA != nil {
			t.Fatalf("logo depois de criar: %+v", v)
		}

		seen := map[order.Status]bool{}
		prevRank, lastMinutes := -1, 1<<30
		rank := map[order.Status]int{order.StatusPending: 0, order.StatusAssigned: 1, order.StatusPickedUp: 2, order.StatusOutForDelivery: 3, order.StatusDelivered: 4, order.StatusFailedAttempt: 4}
		var last tracking.View
		for s := 0; s <= 600; s += 2 {
			v := o.View(t0.Add(time.Duration(s) * time.Second))
			if rank[v.Status] < prevRank {
				t.Fatalf("seed %d, %ds: status voltou para %s", seed, s, v.Status)
			}
			prevRank = rank[v.Status]
			seen[v.Status] = true
			if v.ETA != nil {
				if v.ETA.MinutesLeft > lastMinutes+1 { // +1: um evento novo pode arredondar para cima
					t.Fatalf("seed %d, %ds: ETA subiu de %d para %d min", seed, s, lastMinutes, v.ETA.MinutesLeft)
				}
				lastMinutes = v.ETA.MinutesLeft
			}
			last = v
		}

		for _, st := range []order.Status{order.StatusAssigned, order.StatusPickedUp, order.StatusOutForDelivery} {
			if !seen[st] {
				t.Errorf("seed %d: a simulação não passou por %s", seed, st)
			}
		}
		if o.Fails() {
			if last.Status != order.StatusFailedAttempt || last.Message != notify.DefaultMessage {
				t.Errorf("seed %d: final %s, mensagem %q", seed, last.Status, last.Message)
			}
		} else if last.Status != order.StatusDelivered {
			t.Errorf("seed %d: final %s, quero delivered", seed, last.Status)
		}
		if last.ETA != nil || !last.Demo || len(last.Timeline) != 5 { // created, assigned, picked_up, out_for_delivery, fim
			t.Errorf("seed %d: view final %+v", seed, last)
		}
	}
}

func TestRandomOrdersStayInDemoRange(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	for range 200 {
		o := RandomOrder(t0, rng)
		if d := geo.DistanceKm(o.Pickup, o.Dropoff); d < 1 || d > 3.5 {
			t.Fatalf("entrega com %.2f km fora do esperado", d)
		}
		if _, err := ParseID(o.ID()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHandler(t *testing.T) {
	h := NewHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/orders", strings.NewReader(
		`{"pickup": {"lat": -23.5505, "lng": -46.6333}, "dropoff": {"lat": -23.5614, "lng": -46.6559}}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body.String())
	}
	var created createResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/orders/"+created.ID+"/tracking", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"demo":true`) {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/orders", nil)) // sem corpo: pontos sorteados
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST sem corpo = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/orders/nao-existe/tracking", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("id inválido = %d, quero 404", rec.Code)
	}
}
