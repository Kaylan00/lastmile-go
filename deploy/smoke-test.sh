#!/usr/bin/env bash
# Teste ponta a ponta do docker-compose: com tudo no ar, cria um pedido pela
# página (nginx em :3000) e espera o simulador entregar e o tracking mostrar.
#
#   SIM_SPEED_KMH=600 docker compose up -d --build
#   ./deploy/smoke-test.sh
#
# O caminho testado: página -> dispatch (pedido) -> worker (SKIP LOCKED) ->
# outbox -> Pub/Sub (emulador) -> tracking -> página.
set -euo pipefail

BASE=${BASE:-http://localhost:3000}
TIMEOUT=${TIMEOUT:-180} # segundos

deadline=$((SECONDS + TIMEOUT))
check_deadline() {
  if ((SECONDS > deadline)); then
    echo "FALHOU: passou de ${TIMEOUT}s ($1)"
    docker compose logs --tail 30 dispatch tracking simulator || true
    exit 1
  fi
}

echo "== esperando os serviços ficarem prontos"
until curl -sf localhost:8080/ready >/dev/null && curl -sf localhost:8081/ready >/dev/null && curl -sf "$BASE/" >/dev/null; do
  check_deadline "serviços não ficaram prontos"
  sleep 2
done

echo "== criando pedido pela página"
order=$(curl -sf -X POST "$BASE/api/orders" -H 'Content-Type: application/json' \
  -d '{"pickup": {"lat": -23.5505, "lng": -46.6333}, "dropoff": {"lat": -23.5614, "lng": -46.6559}}')
id=$(echo "$order" | jq -r .id)
echo "pedido $id"

echo "== acompanhando o rastreio"
last=""
while true; do
  check_deadline "pedido não terminou (último status: ${last:-nenhum})"
  status=$(curl -s "$BASE/api/orders/$id/tracking" | jq -r '.status // empty')
  if [[ "$status" != "$last" ]]; then
    echo "status: ${status:-(ainda sem evento)}"
    last=$status
  fi
  case "$status" in
    delivered | failed_attempt)
      curl -s "$BASE/api/orders/$id/tracking" | jq '{status, driver_id, message, timeline: [.timeline[].type]}'
      echo "OK: pedido percorreu o sistema inteiro"
      exit 0
      ;;
  esac
  sleep 2
done
