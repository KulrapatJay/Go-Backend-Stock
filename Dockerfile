# syntax=docker/dockerfile:1

FROM golang:1.27.1-bookworm@sha256:8d48e12ec56735e9358640898b9d9b9fcca110612ed8a5567438c0a1baa24e66 AS base
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Dev: source is bind-mounted by docker-compose.override.yml and rebuilt by air.
FROM base AS dev
ARG AIR_VERSION
RUN go install github.com/air-verse/air@${AIR_VERSION}
CMD ["air", "-c", ".air.toml"]

FROM base AS build
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM base AS goose-build
ARG GOOSE_VERSION=v3.28.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go install \
      -tags 'no_clickhouse no_duckdb no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb' \
      github.com/pressly/goose/v3/cmd/goose@${GOOSE_VERSION}

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS migrate
COPY --from=goose-build /go/bin/goose /usr/local/bin/goose
COPY migrations /migrations
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/goose"]
CMD ["-dir", "/migrations", "-table", "app.goose_db_version", "up"]

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS runtime
COPY --from=build /out/api /usr/local/bin/api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
