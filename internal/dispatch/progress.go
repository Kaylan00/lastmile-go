package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

// Progress é uma etapa da entrega informada pelo app do motorista.
type Progress struct {
	OrderID  string
	DriverID string
	Status   order.Status // picked_up, out_for_delivery, delivered ou failed_attempt
	Position *geo.Point   // onde o motorista está (opcional)
	Note     string       // ocorrência, ex.: "cliente ausente" (usado no failed_attempt)
}

// ReportProgress muda o status do pedido e grava o evento na outbox, na mesma
// transação. Quando a entrega termina, o motorista volta a ficar livre.
//
// Se o app do motorista mandar a mesma etapa duas vezes (retry depois de um
// timeout, por exemplo), a segunda não muda nada e não gera evento: changed = false.
func (s *Store) ReportProgress(ctx context.Context, p Progress) (o Order, changed bool, err error) {
	if !validOrderID(p.OrderID) {
		return Order{}, false, ErrNotFound
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Order{}, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// FOR UPDATE: duas etapas do mesmo pedido chegando juntas são processadas uma de cada vez.
	o, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = $1 FOR UPDATE`, p.OrderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, false, ErrNotFound
	}
	if err != nil {
		return Order{}, false, fmt.Errorf("buscando pedido: %w", err)
	}
	if o.DriverID != p.DriverID {
		return Order{}, false, ErrWrongDriver
	}
	if o.Status == p.Status {
		return o, false, nil
	}
	if !order.CanTransition(o.Status, p.Status) {
		return Order{}, false, fmt.Errorf("%w: de %s para %s", ErrInvalidTransition, o.Status, p.Status)
	}

	if _, err := tx.Exec(ctx, `UPDATE orders SET status = $1, updated_at = now() WHERE id = $2`, p.Status, p.OrderID); err != nil {
		return Order{}, false, fmt.Errorf("atualizando pedido: %w", err)
	}

	var lat, lng *float64
	if p.Position != nil {
		lat, lng = &p.Position.Lat, &p.Position.Lng
	}
	if p.Status.Terminal() {
		// fim da entrega: o motorista fica livre de novo, na posição onde está
		_, err = tx.Exec(ctx, `
			UPDATE drivers
			   SET status = 'available', current_order_id = NULL,
			       lat = COALESCE($2, lat), lng = COALESCE($3, lng), updated_at = now()
			 WHERE id = $1`, p.DriverID, lat, lng)
	} else if p.Position != nil {
		_, err = tx.Exec(ctx, `UPDATE drivers SET lat = $2, lng = $3, updated_at = now() WHERE id = $1`,
			p.DriverID, p.Position.Lat, p.Position.Lng)
	}
	if err != nil {
		return Order{}, false, fmt.Errorf("atualizando motorista: %w", err)
	}

	typ, _ := events.TypeFor(p.Status)
	if err := enqueue(ctx, tx, events.Event{
		ID:         events.NewID(),
		Type:       typ,
		OrderID:    o.ID,
		DriverID:   o.DriverID,
		OccurredAt: events.Now(),
		Position:   p.Position,
		Pickup:     o.Pickup,
		Dropoff:    o.Dropoff,
		Note:       p.Note,
	}); err != nil {
		return Order{}, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Order{}, false, fmt.Errorf("commit: %w", err)
	}
	o.Status = p.Status
	return o, true, nil
}

// UpdateLocation grava a posição atual do motorista. Se ele estiver com um
// pedido, gera o evento order.location_updated (o tracking recalcula o ETA).
func (s *Store) UpdateLocation(ctx context.Context, driverID string, pos geo.Point) (Driver, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Driver{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	d, err := scanDriver(tx.QueryRow(ctx, `
		UPDATE drivers SET lat = $2, lng = $3, updated_at = now()
		 WHERE id = $1
		RETURNING `+driverColumns, driverID, pos.Lat, pos.Lng))
	if errors.Is(err, pgx.ErrNoRows) {
		return Driver{}, ErrNotFound
	}
	if err != nil {
		return Driver{}, fmt.Errorf("atualizando posição: %w", err)
	}

	if d.CurrentOrderID != "" {
		var pickup, dropoff geo.Point
		if err := tx.QueryRow(ctx, `
			SELECT pickup_lat, pickup_lng, dropoff_lat, dropoff_lng FROM orders WHERE id = $1`,
			d.CurrentOrderID).Scan(&pickup.Lat, &pickup.Lng, &dropoff.Lat, &dropoff.Lng); err != nil {
			return Driver{}, fmt.Errorf("buscando pedido atual: %w", err)
		}
		if err := enqueue(ctx, tx, events.Event{
			ID:         events.NewID(),
			Type:       events.OrderLocation,
			OrderID:    d.CurrentOrderID,
			DriverID:   d.ID,
			OccurredAt: events.Now(),
			Position:   &pos,
			Pickup:     pickup,
			Dropoff:    dropoff,
		}); err != nil {
			return Driver{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Driver{}, fmt.Errorf("commit: %w", err)
	}
	return d, nil
}
