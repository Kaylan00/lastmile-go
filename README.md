# lastmile-go

Microsserviços em Go para entrega de última milha: **despacho** de pedidos para motoristas e **rastreio** da entrega
com previsão de chegada para o comprador. Google Cloud (Cloud Run e Pub/Sub), PostgreSQL e uma página React.

> Em construção. O plano completo está em [`CLAUDE.md`](CLAUDE.md) e em [`docs/ESCOPO.md`](docs/ESCOPO.md).

## Serviços
- `dispatch-service`: aloca cada pedido ao motorista livre mais próximo, sem alocação dupla (`FOR UPDATE SKIP LOCKED`).
- `tracking-service`: consome os eventos da rota pelo Pub/Sub (idempotente, com retry e dead-letter) e expõe status e ETA.
