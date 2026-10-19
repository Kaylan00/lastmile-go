-- O estado atual de cada pedido, do ponto de vista do comprador.
CREATE TABLE order_tracking (
    order_id      uuid PRIMARY KEY,
    status        text NOT NULL
                  CHECK (status IN ('pending', 'assigned', 'picked_up', 'out_for_delivery',
                                    'delivered', 'failed_attempt')),
    status_at     timestamptz,
    driver_id     text,
    pickup_lat    float8 NOT NULL,
    pickup_lng    float8 NOT NULL,
    dropoff_lat   float8 NOT NULL,
    dropoff_lng   float8 NOT NULL,
    driver_lat    float8,
    driver_lng    float8,
    position_at   timestamptz,
    buyer_message text,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Idempotência: cada event_id é processado uma vez só. O Pub/Sub entrega
-- "pelo menos uma vez", então a mesma mensagem pode chegar repetida, inclusive
-- em duas réplicas ao mesmo tempo. A chave primária é quem garante: o segundo
-- INSERT do mesmo event_id não entra (ON CONFLICT DO NOTHING).
-- A tabela também serve de linha do tempo do pedido.
CREATE TABLE processed_events (
    event_id     uuid PRIMARY KEY,
    order_id     uuid NOT NULL,
    event_type   text NOT NULL,
    occurred_at  timestamptz NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX processed_events_order_idx ON processed_events (order_id, occurred_at);
