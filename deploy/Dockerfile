# Uma imagem por serviço, a partir do mesmo Dockerfile:
#   docker build -f deploy/Dockerfile --build-arg SERVICE=dispatch -t lastmile-dispatch .
#   docker build -f deploy/Dockerfile --build-arg SERVICE=tracking -t lastmile-tracking .

# 1) build: compila um binário estático (CGO_ENABLED=0 => não depende da libc)
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICE
RUN test -n "$SERVICE" || (echo "passe --build-arg SERVICE=dispatch|tracking|simulator" && exit 1)
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${SERVICE}

# 2) runtime: distroless não tem shell nem gerenciador de pacotes (imagem pequena e
#    com pouca superfície de ataque) e roda como usuário sem privilégio.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
