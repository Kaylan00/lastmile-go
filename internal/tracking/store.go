package tracking

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/geo"
	"github.com/Kaylan00/lastmile-go/internal/order"
)

// ErrNotFound: o tracking ainda não recebeu nenhum evento deste pedido.
var ErrNotFound = errors.New("pedido não encontrado")

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Store guarda o estado de rastreio no Postgres.
type Store struct {
	db *pgxpool.Pool
}

// NewStore cria o Store.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// Result diz o que aconteceu com um evento.
type Result struct {
	Duplicate bool         // o event_id já tinha sido processado: nada foi feito
	Changed   bool         // o estado do pedido mudou
	Status    order.Status // status depois do evento
}

// AlreadyProcessed diz se o evento já foi aplicado. É só uma checagem rápida
// para evitar trabalho caro (como chamar o LLM) em mensagem repetida; a garantia
// de verdade é a chave primária dentro de Process.
func (s *Store) AlreadyProcessed(ctx context.Context, eventID string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM processed_events WHERE event_id = $1)`, eventID).Scan(&exists)
	return exists, err
}

// Process aplica o evento uma única vez, numa transação:
//
//  1. registra o event_id em processed_events (se já existia, é duplicado e para aqui);
//  2. trava a linha do pedido (FOR UPDATE), para eventos do mesmo pedido que
//     chegam ao mesmo tempo serem aplicados um depois do outro;
//  3. aplica o evento ao estado (Apply) e grava se mudou.
//
// buyerMessage é gravada se este evento levar o pedido a failed_attempt.
func (s *Store) Process(ctx context.Context, e events.Event, buyerMessage string) (Result, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	tag, err := tx.Exec(ctx, `
		INSERT INTO processed_events (event_id, order_id, event_type, occurred_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id) DO NOTHING`,
		e.ID, e.OrderID, e.Type, e.OccurredAt)
	if err != nil {
		return Result{}, fmt.Errorf("registrando evento: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Result{Duplicate: true}, nil // já processado antes: o rollback não muda nada
	}

	// Garante que a linha existe (o primeiro evento do pedido cria) e depois trava.
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_tracking (order_id, status, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng)
		VALUES ($1, 'pending', $2, $3, $4, $5)
		ON CONFLICT (order_id) DO NOTHING`,
		e.OrderID, e.Pickup.Lat, e.Pickup.Lng, e.Dropoff.Lat, e.Dropoff.Lng); err != nil {
		return Result{}, fmt.Errorf("criando rastreio: %w", err)
	}
	before, err := scanState(tx.QueryRow(ctx, `SELECT `+stateColumns+` FROM order_tracking WHERE order_id = $1 FOR UPDATE`, e.OrderID))
	if err != nil {
		return Result{}, fmt.Errorf("lendo rastreio: %w", err)
	}

	after, changed := Apply(before, e)
	if after.Status == order.StatusFailedAttempt && before.Status != order.StatusFailedAttempt {
		after.BuyerMessage = buyerMessage
	}
	if changed {
		var lat, lng *float64
		if after.Driver != nil {
			lat, lng = &after.Driver.Lat, &after.Driver.Lng
		}
		if _, err := tx.Exec(ctx, `
			UPDATE order_tracking
			   SET status = $2, status_at = $3, driver_id = $4, driver_lat = $5, driver_lng = $6,
			       position_at = $7, buyer_message = $8, updated_at = now()
			 WHERE order_id = $1`,
			after.OrderID, after.Status, nullTime(after.StatusAt), nullString(after.DriverID), lat, lng,
			nullTime(after.PositionAt), nullString(after.BuyerMessage)); err != nil {
			return Result{}, fmt.Errorf("atualizando rastreio: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return Result{Changed: changed, Status: after.Status}, nil
}

// Get devolve o estado do pedido e a linha do tempo (sem os eventos de posição).
func (s *Store) Get(ctx context.Context, orderID string) (State, []TimelineEntry, error) {
	if !uuidRe.MatchString(orderID) {
		return State{}, nil, ErrNotFound
	}
	st, err := scanState(s.db.QueryRow(ctx, `SELECT `+stateColumns+` FROM order_tracking WHERE order_id = $1`, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return State{}, nil, ErrNotFound
	}
	if err != nil {
		return State{}, nil, fmt.Errorf("lendo rastreio: %w", err)
	}

	rows, err := s.db.Query(ctx, `
		SELECT event_type, occurred_at
		  FROM processed_events
		 WHERE order_id = $1 AND event_type <> $2
		 ORDER BY occurred_at`, orderID, events.OrderLocation)
	if err != nil {
		return State{}, nil, fmt.Errorf("lendo linha do tempo: %w", err)
	}
	timeline, err := pgx.CollectRows(rows, pgx.RowToStructByPos[TimelineEntry])
	if err != nil {
		return State{}, nil, fmt.Errorf("lendo linha do tempo: %w", err)
	}
	return st, timeline, nil
}

const stateColumns = `order_id, status, status_at, driver_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
	driver_lat, driver_lng, position_at, buyer_message`

func scanState(row pgx.Row) (State, error) {
	var s State
	var statusAt, positionAt *time.Time
	var driverID, message *string
	var lat, lng *float64
	err := row.Scan(&s.OrderID, &s.Status, &statusAt, &driverID, &s.Pickup.Lat, &s.Pickup.Lng,
		&s.Dropoff.Lat, &s.Dropoff.Lng, &lat, &lng, &positionAt, &message)
	if err != nil {
		return State{}, err
	}
	if statusAt != nil {
		s.StatusAt = statusAt.UTC()
	}
	if positionAt != nil {
		s.PositionAt = positionAt.UTC()
	}
	if driverID != nil {
		s.DriverID = *driverID
	}
	if message != nil {
		s.BuyerMessage = *message
	}
	if lat != nil && lng != nil {
		s.Driver = &geo.Point{Lat: *lat, Lng: *lng}
	}
	return s, nil
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
