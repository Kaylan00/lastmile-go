-- Distância em km entre dois pontos (fórmula de Haversine). É a mesma conta de
-- geo.DistanceKm no Go, com o mesmo raio da Terra (6371 km). least(1, ...)
-- protege contra arredondamento que deixaria o argumento do asin acima de 1.
CREATE FUNCTION haversine_km(lat1 float8, lng1 float8, lat2 float8, lng2 float8)
RETURNS float8
LANGUAGE sql IMMUTABLE PARALLEL SAFE
AS $$
  SELECT 2 * 6371.0 * asin(least(1, sqrt(
    power(sin(radians(lat2 - lat1) / 2), 2)
    + cos(radians(lat1)) * cos(radians(lat2)) * power(sin(radians(lng2 - lng1) / 2), 2)
  )))
$$;

CREATE TABLE drivers (
    id               text PRIMARY KEY,
    status           text NOT NULL DEFAULT 'offline'
                     CHECK (status IN ('offline', 'available', 'busy')),
    lat              float8,
    lng              float8,
    current_order_id uuid,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    -- motorista livre precisa ter posição (senão não dá para calcular distância)
    CONSTRAINT drivers_available_has_position
        CHECK (status <> 'available' OR (lat IS NOT NULL AND lng IS NOT NULL)),
    -- ocupado <=> tem pedido atual
    CONSTRAINT drivers_busy_has_order
        CHECK ((status = 'busy') = (current_order_id IS NOT NULL))
);

-- Índice parcial: só as linhas de motoristas livres, que é o que a alocação procura.
CREATE INDEX drivers_available_idx ON drivers (id) WHERE status = 'available';

CREATE TABLE orders (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    status      text NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'assigned', 'picked_up', 'out_for_delivery',
                                  'delivered', 'failed_attempt')),
    pickup_lat  float8 NOT NULL,
    pickup_lng  float8 NOT NULL,
    dropoff_lat float8 NOT NULL,
    dropoff_lng float8 NOT NULL,
    driver_id   text REFERENCES drivers (id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    assigned_at timestamptz,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    -- pendente <=> sem motorista
    CONSTRAINT orders_pending_has_no_driver
        CHECK ((status = 'pending') = (driver_id IS NULL))
);

-- A fila: pedidos pendentes do mais antigo para o mais novo.
CREATE INDEX orders_pending_idx ON orders (created_at) WHERE status = 'pending';

-- Defesa extra no próprio banco: um motorista nunca tem dois pedidos ativos.
-- Mesmo que o código tivesse um bug, o INSERT/UPDATE que violasse isso falharia.
CREATE UNIQUE INDEX orders_one_active_per_driver ON orders (driver_id)
    WHERE status IN ('assigned', 'picked_up', 'out_for_delivery');

ALTER TABLE drivers
    ADD CONSTRAINT drivers_current_order_fk FOREIGN KEY (current_order_id) REFERENCES orders (id);
