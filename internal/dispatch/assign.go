package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/geo"
)

// Assignment é o resultado de uma alocação.
type Assignment struct {
	OrderID    string
	DriverID   string
	DistanceKm float64 // do motorista até o ponto de coleta, em linha reta
}

// AssignNext pega o pedido pendente mais antigo e entrega ao motorista livre
// mais próximo do ponto de coleta. Tudo numa transação só, junto com o evento
// order.assigned na outbox.
//
// Por que não dá alocação dupla, mesmo com várias goroutines e várias réplicas:
//
//   - FOR UPDATE trava a linha escolhida até o COMMIT/ROLLBACK. Outra transação
//     que tentar travar a mesma linha teria que esperar.
//   - SKIP LOCKED faz a outra transação NÃO esperar: ela pula as linhas travadas
//     e pega a próxima. Dois workers nunca pegam o mesmo pedido nem o mesmo
//     motorista, e nenhum fica parado esperando o outro.
//   - Se o motorista mudou de status entre a leitura e o lock, o Postgres
//     reavalia o WHERE (status = 'available') na versão nova da linha e a descarta.
//
// A garantia está no banco, não num mutex do Go: um mutex só vale dentro de um
// processo, e no Cloud Run rodam várias réplicas (processos) ao mesmo tempo.
func (s *Store) AssignNext(ctx context.Context) (Assignment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Assignment{}, fmt.Errorf("abrindo transação: %w", err)
	}
	// Rollback depois do Commit não faz nada; antes dele, desfaz tudo e solta os locks.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// 1) O pedido pendente mais antigo que ninguém está alocando agora.
	var orderID string
	var pickup, dropoff geo.Point
	err = tx.QueryRow(ctx, `
		SELECT id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng
		  FROM orders
		 WHERE status = 'pending'
		 ORDER BY created_at, id
		 LIMIT 1
		   FOR UPDATE SKIP LOCKED`,
	).Scan(&orderID, &pickup.Lat, &pickup.Lng, &dropoff.Lat, &dropoff.Lng)
	if errors.Is(err, pgx.ErrNoRows) {
		return Assignment{}, ErrNoPendingOrder
	}
	if err != nil {
		return Assignment{}, fmt.Errorf("travando pedido: %w", err)
	}

	// 2) O motorista livre mais próximo do ponto de coleta que ninguém travou.
	a := Assignment{OrderID: orderID}
	var driverPos geo.Point
	err = tx.QueryRow(ctx, `
		SELECT id, lat, lng, haversine_km(lat, lng, $1, $2) AS distance_km
		  FROM drivers
		 WHERE status = 'available'
		 ORDER BY distance_km, id
		 LIMIT 1
		   FOR UPDATE SKIP LOCKED`,
		pickup.Lat, pickup.Lng,
	).Scan(&a.DriverID, &driverPos.Lat, &driverPos.Lng, &a.DistanceKm)
	if errors.Is(err, pgx.ErrNoRows) {
		return Assignment{}, ErrNoDriverAvailable // o defer faz rollback e o pedido volta para a fila
	}
	if err != nil {
		return Assignment{}, fmt.Errorf("travando motorista: %w", err)
	}

	// 3) Grava a alocação nas duas tabelas.
	if _, err := tx.Exec(ctx, `
		UPDATE orders
		   SET status = 'assigned', driver_id = $1, assigned_at = now(), updated_at = now()
		 WHERE id = $2`, a.DriverID, a.OrderID); err != nil {
		return Assignment{}, fmt.Errorf("atualizando pedido: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE drivers
		   SET status = 'busy', current_order_id = $1, updated_at = now()
		 WHERE id = $2`, a.OrderID, a.DriverID); err != nil {
		return Assignment{}, fmt.Errorf("atualizando motorista: %w", err)
	}

	// 4) O evento vai para a outbox na mesma transação (ver outbox.go).
	if err := enqueue(ctx, tx, events.Event{
		ID:         events.NewID(),
		Type:       events.OrderAssigned,
		OrderID:    a.OrderID,
		DriverID:   a.DriverID,
		OccurredAt: events.Now(),
		Position:   &driverPos,
		Pickup:     pickup,
		Dropoff:    dropoff,
	}); err != nil {
		return Assignment{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Assignment{}, fmt.Errorf("commit da alocação: %w", err)
	}
	return a, nil
}
