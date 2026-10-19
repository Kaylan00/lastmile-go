// Package messaging liga os serviços ao Google Cloud Pub/Sub (ou ao emulador
// local, quando a variável PUBSUB_EMULATOR_HOST está definida).
package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Message é o que o serviço publica: os dados (JSON) e atributos (metadados).
type Message struct {
	Data       []byte
	Attributes map[string]string
}

// Publisher publica uma mensagem e espera a confirmação do Pub/Sub.
// É uma interface para os testes poderem usar um fake no lugar do Pub/Sub.
type Publisher interface {
	Publish(ctx context.Context, m Message) error
}

// Connect cria o client do Pub/Sub para o projeto do GCP.
// Com PUBSUB_EMULATOR_HOST definido, a própria biblioteca conecta no emulador.
func Connect(ctx context.Context, projectID string, opts ...option.ClientOption) (*pubsub.Client, error) {
	c, err := pubsub.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("conectando no pub/sub: %w", err)
	}
	return c, nil
}

// TopicPublisher publica num tópico do Pub/Sub.
type TopicPublisher struct {
	p *pubsub.Publisher
}

// NewPublisher cria o publicador de um tópico.
func NewPublisher(c *pubsub.Client, topicID string) *TopicPublisher {
	return &TopicPublisher{p: c.Publisher(topicID)}
}

// Publish manda a mensagem e espera o Pub/Sub confirmar que guardou.
// Só depois disso é seguro marcar o evento como publicado.
func (t *TopicPublisher) Publish(ctx context.Context, m Message) error {
	res := t.p.Publish(ctx, &pubsub.Message{Data: m.Data, Attributes: m.Attributes})
	if _, err := res.Get(ctx); err != nil {
		return fmt.Errorf("publicando no pub/sub: %w", err)
	}
	return nil
}

// Stop envia o que estiver no buffer e encerra as goroutines do publicador.
func (t *TopicPublisher) Stop() { t.p.Stop() }

// Received é uma mensagem entregue pela assinatura.
type Received struct {
	ID         string
	Data       []byte
	Attributes map[string]string
	// DeliveryAttempt é a tentativa de entrega (1, 2, 3...). Só vem preenchido
	// quando a assinatura tem dead-letter configurada; senão fica 0.
	DeliveryAttempt int
}

// Handler processa uma mensagem. Devolver nil confirma (ack); devolver erro
// recusa (nack) e o Pub/Sub entrega de novo mais tarde, com backoff.
type Handler func(ctx context.Context, m Received) error

// ReceiveConfig controla o consumo de uma assinatura.
type ReceiveConfig struct {
	SubscriptionID string
	// MaxOutstanding é o controle de fluxo: no máximo N mensagens sendo
	// processadas ao mesmo tempo. Se o banco ficar lento, o consumidor para de
	// puxar mensagens novas em vez de acumular goroutines.
	MaxOutstanding int
	// ShutdownTimeout é quanto esperar as mensagens em andamento no desligamento.
	ShutdownTimeout time.Duration
}

// Receive consome a assinatura até ctx ser cancelado. A biblioteca chama h em
// várias goroutines ao mesmo tempo (até MaxOutstanding).
func Receive(ctx context.Context, c *pubsub.Client, cfg ReceiveConfig, h Handler) error {
	sub := c.Subscriber(cfg.SubscriptionID)
	sub.ReceiveSettings.MaxOutstandingMessages = cfg.MaxOutstanding
	sub.ReceiveSettings.ShutdownOptions = &pubsub.ShutdownOptions{
		Timeout:  cfg.ShutdownTimeout,
		Behavior: pubsub.ShutdownBehaviorWaitForProcessing,
	}

	err := sub.Receive(ctx, func(ctx context.Context, m *pubsub.Message) {
		r := Received{ID: m.ID, Data: m.Data, Attributes: m.Attributes}
		if m.DeliveryAttempt != nil {
			r.DeliveryAttempt = *m.DeliveryAttempt
		}
		if err := h(ctx, r); err != nil {
			m.Nack()
			return
		}
		m.Ack()
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("consumindo %s: %w", cfg.SubscriptionID, err)
	}
	return nil
}

// TopicName monta o nome completo do tópico: projects/<projeto>/topics/<id>.
func TopicName(projectID, topicID string) string {
	return fmt.Sprintf("projects/%s/topics/%s", projectID, topicID)
}

// SubscriptionName monta o nome completo da assinatura.
func SubscriptionName(projectID, subID string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
}

// EnsureTopic cria o tópico se ele não existir. Usado no emulador e em
// desenvolvimento; na nuvem os tópicos são criados pelo script de deploy.
func EnsureTopic(ctx context.Context, c *pubsub.Client, topicID string) error {
	_, err := c.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{Name: TopicName(c.Project(), topicID)})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return fmt.Errorf("criando tópico %s: %w", topicID, err)
	}
	return nil
}

// SubscriptionConfig descreve uma assinatura com retry e dead-letter.
type SubscriptionConfig struct {
	ID          string
	TopicID     string
	AckDeadline time.Duration
	// Retry: depois de um nack, o Pub/Sub espera entre MinBackoff e MaxBackoff
	// (crescendo exponencialmente) antes de entregar de novo.
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// Dead-letter: depois de MaxDeliveryAttempts entregas sem ack, a mensagem
	// vai para DeadLetterTopicID em vez de voltar para a fila. Vazio = desligado.
	DeadLetterTopicID   string
	MaxDeliveryAttempts int
}

// EnsureSubscription cria a assinatura se ela não existir.
func EnsureSubscription(ctx context.Context, c *pubsub.Client, cfg SubscriptionConfig) error {
	sub := &pubsubpb.Subscription{
		Name:               SubscriptionName(c.Project(), cfg.ID),
		Topic:              TopicName(c.Project(), cfg.TopicID),
		AckDeadlineSeconds: int32(cfg.AckDeadline.Seconds()),
	}
	if cfg.MinBackoff > 0 {
		sub.RetryPolicy = &pubsubpb.RetryPolicy{
			MinimumBackoff: durationpb.New(cfg.MinBackoff),
			MaximumBackoff: durationpb.New(cfg.MaxBackoff),
		}
	}
	if cfg.DeadLetterTopicID != "" {
		sub.DeadLetterPolicy = &pubsubpb.DeadLetterPolicy{
			DeadLetterTopic:     TopicName(c.Project(), cfg.DeadLetterTopicID),
			MaxDeliveryAttempts: int32(cfg.MaxDeliveryAttempts),
		}
	}
	_, err := c.SubscriptionAdminClient.CreateSubscription(ctx, sub)
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return fmt.Errorf("criando assinatura %s: %w", cfg.ID, err)
	}
	return nil
}
