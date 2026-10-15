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
