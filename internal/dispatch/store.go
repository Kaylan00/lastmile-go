package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/geo"
)

// Store faz as leituras e escritas de pedidos e motoristas no Postgres.
type Store struct {
	db *pgxpool.Pool
}

// NewStore cria o Store.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

const orderColumns = `id, status, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
	driver_id, created_at, assigned_at`

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	var driverID *string
	err := row.Scan(&o.ID, &o.Status, &o.Pickup.Lat, &o.Pickup.Lng, &o.Dropoff.Lat, &o.Dropoff.Lng,
		&driverID, &o.CreatedAt, &o.AssignedAt)
	if driverID != nil {
		o.DriverID = *driverID
	}
	return o, err
}

const driverColumns = `id, status, lat, lng, current_order_id, updated_at`

func scanDriver(row pgx.Row) (Driver, error) {
	var d Driver
	var lat, lng *float64
	var orderID *string
	if err := row.Scan(&d.ID, &d.Status, &lat, &lng, &orderID, &d.UpdatedAt); err != nil {
		return Driver{}, err
	}
	if lat != nil && lng != nil {
		d.Position = &geo.Point{Lat: *lat, Lng: *lng}
	}
	if orderID != nil {
		d.CurrentOrderID = *orderID
	}
	return d, nil
}

// CreateOrder grava um pedido novo com status pending e o evento order.created
// na outbox, na mesma transação. Assim o comprador já acompanha o pedido
// enquanto ele espera motorista.
func (s *Store) CreateOrder(ctx context.Context, pickup, dropoff geo.Point) (Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Order{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	o, err := scanOrder(tx.QueryRow(ctx, `
		INSERT INTO orders (pickup_lat, pickup_lng, dropoff_lat, dropoff_lng)
		VALUES ($1, $2, $3, $4)
		RETURNING `+orderColumns,
		pickup.Lat, pickup.Lng, dropoff.Lat, dropoff.Lng))
	if err != nil {
		return Order{}, fmt.Errorf("criando pedido: %w", err)
	}
	if err := enqueue(ctx, tx, events.Event{
		ID:         events.NewID(),
		Type:       events.OrderCreated,
		OrderID:    o.ID,
		OccurredAt: o.CreatedAt.UTC(),
		Pickup:     o.Pickup,
		Dropoff:    o.Dropoff,
	}); err != nil {
		return Order{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, fmt.Errorf("commit: %w", err)
	}
	return o, nil
}

// GetOrder busca um pedido pelo id.
func (s *Store) GetOrder(ctx context.Context, id string) (Order, error) {
	if !validOrderID(id) {
		return Order{}, ErrNotFound
	}
	o, err := scanOrder(s.db.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("buscando pedido: %w", err)
	}
	return o, nil
}

// SetAvailability marca o motorista como livre (com a posição atual) ou offline.
// Cria o motorista se ele ainda não existir. Motorista ocupado não pode mudar
// de situação por aqui: ele fica livre de novo ao terminar a entrega.
func (s *Store) SetAvailability(ctx context.Context, id string, available bool, pos *geo.Point) (Driver, error) {
	status := DriverOffline
	if available {
		status = DriverAvailable
	}
	var lat, lng *float64
	if pos != nil {
		lat, lng = &pos.Lat, &pos.Lng
	}

	// O WHERE no ON CONFLICT faz o UPDATE não acontecer se o motorista estiver
	// ocupado; aí nenhuma linha volta e sabemos que ele está busy.
	// COALESCE mantém a última posição conhecida quando ele fica offline sem mandar posição.
	row := s.db.QueryRow(ctx, `
		INSERT INTO drivers (id, status, lat, lng)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE
		   SET status = EXCLUDED.status,
		       lat = COALESCE(EXCLUDED.lat, drivers.lat),
		       lng = COALESCE(EXCLUDED.lng, drivers.lng),
		       updated_at = now()
		 WHERE drivers.status <> 'busy'
		RETURNING `+driverColumns,
		id, status, lat, lng)
	d, err := scanDriver(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Driver{}, ErrDriverBusy
	}
	if err != nil {
		return Driver{}, fmt.Errorf("atualizando motorista: %w", err)
	}
	return d, nil
}

// GetDriver busca um motorista pelo id.
func (s *Store) GetDriver(ctx context.Context, id string) (Driver, error) {
	d, err := scanDriver(s.db.QueryRow(ctx, `SELECT `+driverColumns+` FROM drivers WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Driver{}, ErrNotFound
	}
	if err != nil {
		return Driver{}, fmt.Errorf("buscando motorista: %w", err)
	}
	return d, nil
}
