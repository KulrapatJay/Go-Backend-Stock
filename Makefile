-include .env
export

BIN := $(CURDIR)/bin

.PHONY: test vet tidy-check

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

tidy-check:
	go mod tidy -diff

SWAG_VERSION := v1.16.6
GO_SWAGGER_VERSION := v0.34.1
GOOSE_VERSION := v3.28.0
GOLANGCI_LINT_VERSION := v2.14.0
GOOSE_TAGS := no_clickhouse no_duckdb no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb

SWAG := $(BIN)/swag
SWAGGER := $(BIN)/swagger
GOOSE := $(BIN)/goose
GOLANGCI_LINT := $(BIN)/golangci-lint

.PHONY: tools swagger

tools:
	GOBIN=$(BIN) go install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)
	GOBIN=$(BIN) go install github.com/go-swagger/go-swagger/cmd/swagger@$(GO_SWAGGER_VERSION)
	CGO_ENABLED=0 GOBIN=$(BIN) go install -tags '$(GOOSE_TAGS)' github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

swagger:
	$(SWAG) init -g cmd/api/main.go -d . --parseInternal -o docs/api --outputTypes go,json,yaml
	$(SWAGGER) validate docs/api/swagger.yaml

POSTGRES_HOST_PORT ?= 55432
LOCAL_DB_HOST := 127.0.0.1:$(POSTGRES_HOST_PORT)
MIGRATION_DATABASE_URL ?= postgres://app_migrator:$(APP_MIGRATOR_PASSWORD)@$(LOCAL_DB_HOST)/$(POSTGRES_DB)?sslmode=disable
TEST_ADMIN_DATABASE_URL ?= postgres://$(POSTGRES_ADMIN_USER):$(POSTGRES_ADMIN_PASSWORD)@$(LOCAL_DB_HOST)/$(POSTGRES_DB)?sslmode=disable
TEST_RUNTIME_DATABASE_URL ?= postgres://app_runtime:$(APP_RUNTIME_PASSWORD)@$(LOCAL_DB_HOST)/$(POSTGRES_DB)?sslmode=disable

# `export` above would leak these into every recipe, so a plain `make test` would try to reach a
# database instead of skipping integration tests. test-integration passes them explicitly.
unexport TEST_ADMIN_DATABASE_URL TEST_RUNTIME_DATABASE_URL MIGRATION_DATABASE_URL

.PHONY: migrate-up migrate-status test-integration

migrate-up:
	$(GOOSE) -dir migrations -table app.goose_db_version postgres "$(MIGRATION_DATABASE_URL)" up

migrate-status:
	$(GOOSE) -dir migrations -table app.goose_db_version postgres "$(MIGRATION_DATABASE_URL)" status

test-integration:
	REQUIRE_INTEGRATION=1 \
	TEST_ADMIN_DATABASE_URL="$(TEST_ADMIN_DATABASE_URL)" \
	TEST_RUNTIME_DATABASE_URL="$(TEST_RUNTIME_DATABASE_URL)" \
	go test -race -count=1 ./internal/adapter/postgres/...

.PHONY: lint-install lint check

lint-install:
	GOBIN=$(BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

lint:
	$(GOLANGCI_LINT) run ./...

check: tidy-check vet lint swagger test
	git diff --exit-code -- docs/api
