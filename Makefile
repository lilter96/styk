SHELL := /bin/sh
export GOTOOLCHAIN := go1.27.3
-include .env
export
GOOSE := go run -tags="no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql" github.com/pressly/goose/v3/cmd/goose@v3.25.0
.PHONY: up down migrate test test-integration lint build
up:
	docker compose up -d --wait
down:
	docker compose down
migrate:
	@$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" up
test:
	go test ./...
test-integration:
	go test -tags=integration -count=1 ./tests/integration/...
lint:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))" || (gofmt -l $$(find . -name '*.go' -not -path './vendor/*'); exit 1)
	go vet ./...
build:
	mkdir -p bin
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker
	go build -o bin/demo ./cmd/demo
	go build -o bin/moysklad-import ./cmd/moysklad-import
	go build -o bin/ozon-import ./cmd/ozon-import
	go build -o bin/matching ./cmd/matching
	go build -o bin/warehouse-mapping ./cmd/warehouse-mapping
