# 1) build da página com Node
FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

# 2) nginx serve os arquivos estáticos e repassa /api para os serviços.
#    Os valores abaixo são os do docker-compose; no Cloud Run, PORT vem da
#    plataforma e as URLs são passadas no deploy.
FROM nginx:1.27-alpine
ENV PORT=80 \
    DISPATCH_URL=http://dispatch:8080 \
    TRACKING_URL=http://tracking:8081
COPY nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
