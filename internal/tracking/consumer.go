package tracking

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/Kaylan00/lastmile-go/internal/events"
	"github.com/Kaylan00/lastmile-go/internal/notify"
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
	store    *Store
	dlq      messaging.Publisher
	notifier *notify.Notifier
	log      *slog.Logger

	ProcessTimeout time.Duration // prazo para processar uma mensagem
}

// NewConsumer cria o consumidor. dlq publica no tópico de dead-letter e
// notifier escreve a mensagem do comprador quando a entrega falha.
func NewConsumer(store *Store, dlq messaging.Publisher, notifier *notify.Notifier, log *slog.Logger) *Consumer {
	return &Consumer{store: store, dlq: dlq, notifier: notifier, log: log, ProcessTimeout: 10 * time.Second}
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

	message, err := c.buyerMessage(pctx, e)
	if err != nil {
		return err
	}

	res, err := c.store.Process(pctx, e, message)
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

// buyerMessage chama o LLM (via notify) só para eventos de tentativa falha.
//
// A chamada acontece ANTES da transação de Process, para não segurar o lock da
// linha do pedido enquanto espera o LLM (até 3 s). Antes, uma checagem rápida
// evita pagar uma chamada ao LLM por uma mensagem repetida.
func (c *Consumer) buyerMessage(ctx context.Context, e events.Event) (string, error) {
	if e.Type != events.OrderFailedAttempt {
		return "", nil
	}
	done, err := c.store.AlreadyProcessed(ctx, e.ID)
	if err != nil {
		return "", err
	}
	if done {
		return "", nil
	}
	msg, source := c.notifier.BuyerMessage(ctx, e.Note)
	c.log.Info("mensagem para o comprador", "order_id", e.OrderID, "source", source)
	return msg, nil
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
