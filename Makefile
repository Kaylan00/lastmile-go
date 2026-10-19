.PHONY: test vet fmt up down logs

# testes unitários (os de integração são pulados se não houver banco)
test:
	go test -race ./...

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
