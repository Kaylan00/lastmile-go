package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/platform/messaging"
	"github.com/Kaylan00/lastmile-go/internal/platform/retry"
)

// enqueue grava o evento na outbox DENTRO da transação tx.
//
// O problema que isso resolve: "gravei no banco e o Pub/Sub caiu antes de
// publicar". Se a alocação gravasse no banco e depois chamasse o Pub/Sub, uma
// queda entre as duas coisas perderia o evento (o comprador nunca veria o
// pedido sair). Com a outbox, a mudança e o evento são gravados juntos, e o
// Relay publica depois, quantas vezes precisar.
func enqueue(ctx context.Context, tx pgx.Tx, e events.Event) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("serializando evento: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox (event_id, event_type, order_id, payload)
		VALUES ($1, $2, $3, $4)`,
		e.ID, e.Type, e.OrderID, payload)
	if err != nil {
		return fmt.Errorf("gravando evento na outbox: %w", err)
	}
	return nil
}

// Relay lê a outbox e publica no Pub/Sub os eventos ainda não publicados.
//
// A entrega é "pelo menos uma vez": se o relay publicar e cair antes de marcar
// published_at, o evento é publicado de novo. Por isso o consumidor (tracking)
// é idempotente por event_id.
type Relay struct {
	db  *pgxpool.Pool
	pub messaging.Publisher
	log *slog.Logger

	BatchSize      int           // quantos eventos por rodada
	Interval       time.Duration // espera entre rodadas quando a fila esvazia
	PublishTimeout time.Duration // prazo de cada publicação
	Backoff        retry.Policy  // espera depois de falhas seguidas
}

// NewRelay cria o relay com valores padrão razoáveis.
func NewRelay(db *pgxpool.Pool, pub messaging.Publisher, log *slog.Logger) *Relay {
	return &Relay{
		db: db, pub: pub, log: log,
		BatchSize:      50,
		Interval:       200 * time.Millisecond,
		PublishTimeout: 5 * time.Second,
		Backoff:        retry.Policy{Initial: 500 * time.Millisecond, Max: 30 * time.Second},
	}
}

// Run publica em loop até ctx ser cancelado.
func (r *Relay) Run(ctx context.Context) {
	failures := 0
	for {
		// A rodada em andamento termina mesmo se o SIGTERM chegar no meio.
		batchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.PublishTimeout*time.Duration(r.BatchSize))
		n, err := r.PublishPending(batchCtx)
		cancel()

		wait := r.Interval
		switch {
		case err != nil:
			failures++
			wait = r.Backoff.Backoff(failures)
			r.log.Warn("outbox: falha ao publicar, vai tentar de novo", "err", err, "retry_in", wait.String())
		case n == r.BatchSize:
			failures = 0
			wait = 0 // a fila ainda tem eventos: publica o próximo lote já
		default:
			failures = 0
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type outboxRow struct {
	ID        int64
	EventID   string
	EventType string
	OrderID   string
	Payload   []byte
}

// PublishPending publica um lote. Devolve quantos eventos foram publicados.
//
// FOR UPDATE SKIP LOCKED de novo: com várias réplicas, cada relay pega um lote
// diferente e nenhum evento é publicado por dois relays ao mesmo tempo.
func (r *Relay) PublishPending(ctx context.Context) (int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx, `
		SELECT id, event_id, event_type, order_id, payload
		  FROM outbox
		 WHERE published_at IS NULL
		 ORDER BY id
		 LIMIT $1
		   FOR UPDATE SKIP LOCKED`, r.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("lendo outbox: %w", err)
	}
	pending, err := pgx.CollectRows(rows, pgx.RowToStructByPos[outboxRow])
	if err != nil {
		return 0, fmt.Errorf("lendo outbox: %w", err)
	}

	var published []int64
	var pubErr error
	for _, row := range pending {
		pctx, cancel := context.WithTimeout(ctx, r.PublishTimeout)
		err := r.pub.Publish(pctx, messaging.Message{
			Data: row.Payload,
			Attributes: map[string]string{
				"event_id":   row.EventID,
				"event_type": row.EventType,
				"order_id":   row.OrderID,
			},
		})
		cancel()
		if err != nil {
			// Para no primeiro erro: se o Pub/Sub caiu, insistir no resto do lote só gasta tempo.
			pubErr = fmt.Errorf("evento %s: %w", row.EventID, err)
			if _, err := tx.Exec(ctx, `UPDATE outbox SET attempts = attempts + 1, last_error = $2 WHERE id = $1`,
				row.ID, pubErr.Error()); err != nil {
				return 0, err
			}
			break
		}
		published = append(published, row.ID)
	}

	if len(published) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, published); err != nil {
			return 0, fmt.Errorf("marcando publicados: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit da outbox: %w", err)
	}
	if len(published) > 0 {
		r.log.Debug("outbox: eventos publicados", "count", len(published))
	}
	return len(published), pubErr
}
