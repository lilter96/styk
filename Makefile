SHELL := /bin/sh
.DEFAULT_GOAL := up
export GOTOOLCHAIN := go1.27.3
# Test/build entry points must never implicitly load provider credentials.
ifneq ($(filter up down migrate,$(or $(MAKECMDGOALS),up)),)
-include .env
-include .env.local
export
endif
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
INTEGRATION_TEST_TIMEOUT ?= 40m
INTEGRATION_TEST_FLAGS ?=
GOOSE := go run -tags="no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql" github.com/pressly/goose/v3/cmd/goose@v3.25.0
.PHONY: up down migrate test test-integration lint build check-plan check check-go check-web check-ui check-integration check-full

# Without BASE/HEAD, inspect staged, unstaged and untracked changes.
export STYK_CHECK_BASE := $(value BASE)
export STYK_CHECK_HEAD := $(value HEAD)
check-plan:
	go run ./cmd/checks plan
check:
	go run ./cmd/checks run --lane fast
check-go:
	go run ./cmd/checks run --lane go
check-web:
	go run ./cmd/checks run --lane web
check-ui:
	go run ./cmd/checks run --lane browser
check-integration:
	go run ./cmd/checks run --lane integration
check-full:
	go run ./cmd/checks run --lane full
up:
	docker compose up -d --wait
down:
	docker compose down
migrate:
	@$(GOOSE) -dir migrations postgres "$(or $(DATABASE_URL_UNPOOLED),$(DATABASE_URL))" up
test:
	go test ./...
test-integration:
	# Live provider checks run separately with an explicit request ledger.
	env -u DATABASE_URL -u STYK_CREDENTIAL_ENCRYPTION_KEY -u STYK_OPERATOR_SECRET -u STYK_LIVE_ORDER_FLOW_TEST -u STYK_LIVE_OZON_FULFILLMENT_TEST -u MOYSKLAD_TOKEN -u MOYSKLAD_ACCOUNT_ID -u OZON_API_KEY -u OZON_CLIENT_ID go test -timeout=$(INTEGRATION_TEST_TIMEOUT) -tags=integration -count=1 $(INTEGRATION_TEST_FLAGS) ./tests/integration/...
lint:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))" || (gofmt -l $$(find . -name '*.go' -not -path './vendor/*'); exit 1)
	go vet ./...
	$(GOLANGCI_LINT) run --max-issues-per-linter=0 --max-same-issues=0 ./...
build:
	mkdir -p bin
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker
	go build -o bin/demo ./cmd/demo
	go build -o bin/moysklad-import ./cmd/moysklad-import
	go build -o bin/ozon-import ./cmd/ozon-import
	go build -o bin/matching ./cmd/matching
	go build -o bin/warehouse-mapping ./cmd/warehouse-mapping
	go build -o bin/ozon-postings ./cmd/ozon-postings
	go build -o bin/stock-commitment ./cmd/stock-commitment
	go build -o bin/moysklad-orders ./cmd/moysklad-orders
	go build -o bin/doctor ./cmd/doctor
