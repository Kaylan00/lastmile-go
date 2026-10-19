// Command simulator faz o papel do app dos motoristas no docker-compose.
//
// Ele registra alguns motoristas livres perto da Av. Paulista e, quando o
// dispatch aloca um pedido para um deles, "dirige" em linha reta até a coleta e
// depois até o comprador, mandando a posição e as etapas para o dispatch pela
// mesma API HTTP que um app de verdade usaria.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
	"github.com/Kaylan00/lastmile-go/internal/platform/config"
	"github.com/Kaylan00/lastmile-go/internal/platform/logging"
	"github.com/Kaylan00/lastmile-go/internal/platform/retry"
)

var center = geo.Point{Lat: -23.5614, Lng: -46.6559} // Av. Paulista

type settings struct {
	DispatchURL string
	Drivers     int
	SpeedKmh    float64
	Tick        time.Duration
	FailRate    float64
	OrderEvery  time.Duration // 0 = não cria pedidos sozinho
}

func main() {
	log := logging.New("simulator")
	env := config.FromOS()
	cfg := settings{
		DispatchURL: env.String("DISPATCH_URL", "http://localhost:8080"),
		Drivers:     env.Int("DRIVERS", 6),
		SpeedKmh:    env.Float("SPEED_KMH", 60),
		Tick:        env.Duration("TICK", 2*time.Second),
		FailRate:    env.Float("FAIL_RATE", 0.15),
		OrderEvery:  env.Duration("ORDER_EVERY", 0),
	}
	if err := env.Err(); err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	api := &client{base: cfg.DispatchURL, http: &http.Client{Timeout: 5 * time.Second}}

	// espera o dispatch subir
	err := retry.Do(ctx, retry.Policy{Attempts: 30, Initial: time.Second, Max: 3 * time.Second},
		func(ctx context.Context) error { return api.do(ctx, http.MethodGet, "/health", nil, nil) })
	if err != nil {
		log.Error("dispatch não respondeu", "err", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	for i := range cfg.Drivers {
		d := &driver{
			id:  fmt.Sprintf("moto-%02d", i+1),
			pos: randomNear(center, 3),
			api: api, cfg: cfg,
			log: log.With("driver_id", fmt.Sprintf("moto-%02d", i+1)),
		}
		wg.Go(func() { d.run(ctx) })
	}
	if cfg.OrderEvery > 0 {
		wg.Go(func() { createOrders(ctx, api, cfg.OrderEvery, log) })
	}
	log.Info("simulador rodando", "drivers", cfg.Drivers, "speed_kmh", cfg.SpeedKmh)
	wg.Wait()
}

// ---------- motorista ----------

type driver struct {
	id  string
	pos geo.Point
	api *client
	cfg settings
	log *slog.Logger
}

type driverResp struct {
	Status         string `json:"status"`
	CurrentOrderID string `json:"current_order_id"`
}

type orderResp struct {
	ID      string       `json:"id"`
	Status  order.Status `json:"status"`
	Pickup  geo.Point    `json:"pickup"`
	Dropoff geo.Point    `json:"dropoff"`
}

func (d *driver) run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := d.api.do(ctx, http.MethodPost, "/drivers/"+d.id+"/availability",
			map[string]any{"available": true, "position": d.pos}, nil); err == nil {
			break
		}
		sleep(ctx, 2*time.Second)
	}

	for ctx.Err() == nil {
		var me driverResp
		if err := d.api.do(ctx, http.MethodGet, "/drivers/"+d.id, nil, &me); err == nil &&
			me.Status == "busy" && me.CurrentOrderID != "" {
			if err := d.deliver(ctx, me.CurrentOrderID); err != nil && ctx.Err() == nil {
				d.log.Warn("entrega interrompida", "err", err)
			}
		}
		sleep(ctx, time.Second)
	}
}

func (d *driver) deliver(ctx context.Context, orderID string) error {
	var o orderResp
	if err := d.api.do(ctx, http.MethodGet, "/orders/"+orderID, nil, &o); err != nil {
		return err
	}
	d.log.Info("pedido recebido", "order_id", orderID)

	// Se o simulador reiniciou no meio de uma entrega, continua de onde parou.
	if o.Status == order.StatusAssigned {
		if err := d.driveTo(ctx, o.Pickup); err != nil {
			return err
		}
		if err := d.report(ctx, orderID, order.StatusPickedUp, ""); err != nil {
			return err
		}
		sleep(ctx, 3*time.Second) // colocando o pacote na moto
		o.Status = order.StatusPickedUp
	}
	if o.Status == order.StatusPickedUp {
		if err := d.report(ctx, orderID, order.StatusOutForDelivery, ""); err != nil {
			return err
		}
	}
	if err := d.driveTo(ctx, o.Dropoff); err != nil {
		return err
	}
	if rand.Float64() < d.cfg.FailRate {
		return d.report(ctx, orderID, order.StatusFailedAttempt, "cliente ausente, ninguém atendeu o interfone")
	}
	return d.report(ctx, orderID, order.StatusDelivered, "")
}

// driveTo anda em linha reta até target, mandando a posição a cada Tick.
func (d *driver) driveTo(ctx context.Context, target geo.Point) error {
	step := d.cfg.SpeedKmh * d.cfg.Tick.Hours() // km por tick
	for {
		left := geo.DistanceKm(d.pos, target)
		if left <= step {
			d.pos = target
		} else {
			d.pos = geo.Interpolate(d.pos, target, step/left)
		}
		if err := d.api.do(ctx, http.MethodPost, "/drivers/"+d.id+"/location",
			map[string]any{"position": d.pos}, nil); err != nil {
			d.log.Warn("falha ao mandar posição", "err", err) // o app real tentaria de novo no próximo tick
		}
		if d.pos == target {
			return nil
		}
		if !sleep(ctx, d.cfg.Tick) {
			return ctx.Err()
		}
	}
}

func (d *driver) report(ctx context.Context, orderID string, st order.Status, note string) error {
	err := d.api.do(ctx, http.MethodPost, "/orders/"+orderID+"/events",
		map[string]any{"driver_id": d.id, "status": st, "position": d.pos, "note": note}, nil)
	if err == nil {
		d.log.Info("etapa informada", "order_id", orderID, "status", st)
	}
	return err
}

// ---------- pedidos automáticos (opcional) ----------

func createOrders(ctx context.Context, api *client, every time.Duration, log *slog.Logger) {
	for sleep(ctx, every) {
		pickup := randomNear(center, 2)
		dropoff := randomNear(pickup, 2.5)
		var o orderResp
		if err := api.do(ctx, http.MethodPost, "/orders", map[string]any{"pickup": pickup, "dropoff": dropoff}, &o); err != nil {
			log.Warn("falha ao criar pedido", "err", err)
			continue
		}
		log.Info("pedido criado pelo simulador", "order_id", o.ID)
	}
}

// ---------- utilidades ----------

// randomNear sorteia um ponto a até maxKm de p.
func randomNear(p geo.Point, maxKm float64) geo.Point {
	km := maxKm * math.Sqrt(rand.Float64()) // sqrt: espalha por igual na área do círculo
	angle := rand.Float64() * 2 * math.Pi
	return geo.Point{
		Lat: p.Lat + km*math.Cos(angle)/111.32,
		Lng: p.Lng + km*math.Sin(angle)/(111.32*math.Cos(p.Lat*math.Pi/180)),
	}
}

// sleep espera d ou até ctx ser cancelado. Devolve false se foi cancelado.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type client struct {
	base string
	http *http.Client
}

// do faz uma chamada JSON ao dispatch. Status >= 400 vira erro.
func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, e.Error)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return errors.Join(errors.New("resposta inválida"), err)
	}
	return nil
}
