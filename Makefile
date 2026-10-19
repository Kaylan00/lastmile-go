.PHONY: test test-integration vet fmt up down logs

TEST_DATABASE_URL ?= postgres://lastmile:lastmile@localhost:5432/lastmile?sslmode=disable

# testes unitários (os de integração são pulados se não houver banco)
test:
	go test -race ./...

# todos os testes, inclusive os de integração, contra o Postgres do docker-compose
test-integration:
	docker compose up -d --wait postgres
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race -count=1 ./...

vet:
	go vet ./...

# lista arquivos fora do padrão do gofmt (CI falha se houver algum)
fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f dispatch tracking
