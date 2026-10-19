package tracking

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/platform/messaging"
)

// Consumer processa as mensagens da assinatura do Pub/Sub.
//
// Três destinos possíveis para cada mensagem:
//   - deu certo (ou é duplicada): ack. O Pub/Sub não entrega de novo.
//   - erro temporário (banco fora, timeout): nack. O Pub/Sub entrega de novo
//     com backoff exponencial (retry policy da assinatura) e, depois de
//     MaxDeliveryAttempts tentativas, manda para o tópico de dead-letter.
//   - mensagem inválida (JSON quebrado, campo faltando): tentar de novo nunca
//     vai dar certo, então ela vai direto para a dead-letter e recebe ack.
type Consumer struct {
	store *Store
	dlq   messaging.Publisher
	log   *slog.Logger

	ProcessTimeout time.Duration // prazo para processar uma mensagem
}

// NewConsumer cria o consumidor. dlq publica no tópico de dead-letter.
func NewConsumer(store *Store, dlq messaging.Publisher, log *slog.Logger) *Consumer {
	return &Consumer{store: store, dlq: dlq, log: log, ProcessTimeout: 10 * time.Second}
}

// Handle é o messaging.Handler: devolver nil = ack, devolver erro = nack.
func (c *Consumer) Handle(ctx context.Context, m messaging.Received) error {
	e, err := events.Parse(m.Data)
	if err != nil {
		return c.deadLetter(ctx, m, err)
	}

	// WithoutCancel: no desligamento, a mensagem em andamento termina (até o prazo)
	// em vez de ser cortada no meio.
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.ProcessTimeout)
	defer cancel()

	res, err := c.store.Process(pctx, e, "")
	if err != nil {
		c.log.Warn("falha ao processar evento; o Pub/Sub vai entregar de novo",
			"event_id", e.ID, "type", e.Type, "delivery_attempt", m.DeliveryAttempt, "err", err)
		return err
	}

	switch {
	case res.Duplicate:
		c.log.Info("evento duplicado ignorado", "event_id", e.ID, "type", e.Type, "order_id", e.OrderID)
	case !res.Changed:
		c.log.Info("evento fora de ordem ignorado", "event_id", e.ID, "type", e.Type, "order_id", e.OrderID, "status", res.Status)
	default:
		c.log.Info("evento aplicado", "event_id", e.ID, "type", e.Type, "order_id", e.OrderID, "status", res.Status)
	}
	return nil
}

// deadLetter publica a mensagem inválida no tópico de dead-letter, com o motivo
// num atributo, e confirma (ack) a original. Se nem a dead-letter aceitar,
// devolve erro (nack) para não perder a mensagem.
func (c *Consumer) deadLetter(ctx context.Context, m messaging.Received, reason error) error {
	attrs := maps.Clone(m.Attributes)
	if attrs == nil {
		attrs = map[string]string{}
	}
	attrs["dead_letter_reason"] = reason.Error()
	attrs["original_message_id"] = m.ID

	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.ProcessTimeout)
	defer cancel()
	if err := c.dlq.Publish(pctx, messaging.Message{Data: m.Data, Attributes: attrs}); err != nil {
		return fmt.Errorf("publicando na dead-letter: %w", err)
	}
	c.log.Error("mensagem inválida enviada para a dead-letter", "message_id", m.ID, "reason", reason.Error())
	return nil
}
