-- Padrão outbox: o evento é gravado nesta tabela NA MESMA TRANSAÇÃO que muda o
-- pedido. Ou os dois ficam gravados, ou nenhum. Depois, o relay lê daqui e
-- publica no Pub/Sub. Se o Pub/Sub estiver fora, o evento espera aqui.
CREATE TABLE outbox (
    id           bigserial PRIMARY KEY,
    event_id     uuid NOT NULL UNIQUE,
    event_type   text NOT NULL,
    order_id     uuid NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempts     int NOT NULL DEFAULT 0,
    last_error   text
);

-- O relay só procura o que ainda não foi publicado, na ordem de gravação.
CREATE INDEX outbox_unpublished_idx ON outbox (id) WHERE published_at IS NULL;
