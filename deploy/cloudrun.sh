#!/usr/bin/env bash
# Deploy do lastmile-go no Google Cloud: Pub/Sub + 3 serviços no Cloud Run
# (dispatch, tracking e a página web).
#
# Antes de rodar:
#   1. gcloud instalado e logado (gcloud auth login) e Docker rodando
#   2. um projeto GCP com faturamento ativo (o free tier cobre um projeto pequeno)
#   3. um Postgres para cada serviço (Cloud SQL, Neon ou Supabase) e as URLs
#      guardadas no Secret Manager:
#        printf '%s' 'postgres://...dispatch' | gcloud secrets create dispatch-database-url --data-file=-
#        printf '%s' 'postgres://...tracking' | gcloud secrets create tracking-database-url --data-file=-
#      opcional, para o LLM: gcloud secrets create anthropic-api-key --data-file=-
#
# Uso:
#   PROJECT=meu-projeto ./deploy/cloudrun.sh
set -euo pipefail

PROJECT=${PROJECT:?defina PROJECT=<id do projeto GCP>}
REGION=${REGION:-southamerica-east1} # São Paulo
REPO=${REPO:-lastmile}
TAG=${TAG:-$(git rev-parse --short HEAD)}
REGISTRY="$REGION-docker.pkg.dev/$PROJECT/$REPO"
RUN_SA="lastmile-run@$PROJECT.iam.gserviceaccount.com"

gcloud config set project "$PROJECT"
gcloud services enable run.googleapis.com pubsub.googleapis.com \
  artifactregistry.googleapis.com secretmanager.googleapis.com

echo "== 1. Pub/Sub: tópico, dead-letter e assinatura com retry =="
gcloud pubsub topics create order-events 2>/dev/null || true
gcloud pubsub topics create order-events-dlq 2>/dev/null || true
# tópico sem assinatura descarta as mensagens: esta guarda o que cair na dead-letter
gcloud pubsub subscriptions create order-events-dlq-inspect --topic order-events-dlq 2>/dev/null || true
gcloud pubsub subscriptions create tracking-order-events \
  --topic order-events \
  --ack-deadline 30 \
  --min-retry-delay 1s --max-retry-delay 60s \
  --dead-letter-topic order-events-dlq --max-delivery-attempts 5 2>/dev/null || true

# O agente do próprio Pub/Sub precisa de permissão para mover mensagens para a dead-letter.
PROJECT_NUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')
PUBSUB_AGENT="serviceAccount:service-$PROJECT_NUMBER@gcp-sa-pubsub.iam.gserviceaccount.com"
gcloud pubsub topics add-iam-policy-binding order-events-dlq --member "$PUBSUB_AGENT" --role roles/pubsub.publisher >/dev/null
gcloud pubsub subscriptions add-iam-policy-binding tracking-order-events --member "$PUBSUB_AGENT" --role roles/pubsub.subscriber >/dev/null

echo "== 2. Conta de serviço dos containers (permissão mínima) =="
gcloud iam service-accounts create lastmile-run --display-name "lastmile-go Cloud Run" 2>/dev/null || true
for role in roles/pubsub.publisher roles/pubsub.subscriber roles/secretmanager.secretAccessor; do
  gcloud projects add-iam-policy-binding "$PROJECT" --member "serviceAccount:$RUN_SA" --role "$role" >/dev/null
done

echo "== 3. Imagens no Artifact Registry =="
gcloud artifacts repositories create "$REPO" --repository-format docker --location "$REGION" 2>/dev/null || true
gcloud auth configure-docker "$REGION-docker.pkg.dev" --quiet
for svc in dispatch tracking; do
  docker build -f deploy/Dockerfile --build-arg SERVICE="$svc" -t "$REGISTRY/$svc:$TAG" .
  docker push "$REGISTRY/$svc:$TAG"
done
docker build -t "$REGISTRY/web:$TAG" web
docker push "$REGISTRY/web:$TAG"

echo "== 4. Serviços no Cloud Run =="
# dispatch e tracking têm trabalho em segundo plano (workers + relay da outbox,
# consumidor do Pub/Sub). Com CPU só durante requisições, esse trabalho pararia:
# por isso --no-cpu-throttling e pelo menos 1 instância.
gcloud run deploy dispatch \
  --image "$REGISTRY/dispatch:$TAG" --region "$REGION" --service-account "$RUN_SA" \
  --allow-unauthenticated --no-cpu-throttling --min-instances 1 --max-instances 3 \
  --set-env-vars "GCP_PROJECT=$PROJECT,WORKERS=4,DB_MAX_CONNS=10" \
  --set-secrets "DATABASE_URL=dispatch-database-url:latest"

TRACKING_SECRETS="DATABASE_URL=tracking-database-url:latest"
if gcloud secrets describe anthropic-api-key >/dev/null 2>&1; then
  TRACKING_SECRETS="$TRACKING_SECRETS,ANTHROPIC_API_KEY=anthropic-api-key:latest"
fi
gcloud run deploy tracking \
  --image "$REGISTRY/tracking:$TAG" --region "$REGION" --service-account "$RUN_SA" \
  --allow-unauthenticated --no-cpu-throttling --min-instances 1 --max-instances 3 \
  --set-env-vars "GCP_PROJECT=$PROJECT,CONSUMER_CONCURRENCY=8" \
  --set-secrets "$TRACKING_SECRETS"

DISPATCH_URL=$(gcloud run services describe dispatch --region "$REGION" --format 'value(status.url)')
TRACKING_URL=$(gcloud run services describe tracking --region "$REGION" --format 'value(status.url)')

# a página só serve arquivos e repassa /api: pode escalar a zero
gcloud run deploy web \
  --image "$REGISTRY/web:$TAG" --region "$REGION" \
  --allow-unauthenticated --max-instances 2 \
  --set-env-vars "DISPATCH_URL=$DISPATCH_URL,TRACKING_URL=$TRACKING_URL"

echo
echo "dispatch: $DISPATCH_URL"
echo "tracking: $TRACKING_URL"
echo "página:   $(gcloud run services describe web --region "$REGION" --format 'value(status.url)')"
