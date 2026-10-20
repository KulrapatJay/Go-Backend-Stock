-- +goose Up
CREATE TABLE app.portfolios (
    id          uuid          NOT NULL,
    user_id     uuid          NOT NULL,
    name        varchar(100)  NOT NULL,
    description varchar(1000) NOT NULL DEFAULT '',
    currency    char(3)       NOT NULL,
    version     integer       NOT NULL DEFAULT 1,
    created_at  timestamptz   NOT NULL,
    updated_at  timestamptz   NOT NULL,
    CONSTRAINT portfolios_pkey PRIMARY KEY (id),
    CONSTRAINT portfolios_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT portfolios_name_trimmed CHECK (name = btrim(name) AND length(name) > 0),
    CONSTRAINT portfolios_currency_usd CHECK (currency = 'USD'),
    CONSTRAINT portfolios_version_positive CHECK (version > 0)
);
-- Case-insensitive unique name per user; the database decides conflicts.
CREATE UNIQUE INDEX portfolios_user_name_unique ON app.portfolios (user_id, lower(name));
CREATE INDEX portfolios_user_created_idx ON app.portfolios (user_id, created_at, id);

CREATE TABLE app.instruments (
    id                uuid         NOT NULL,
    symbol            varchar(15)  NOT NULL,
    provider_asset_id varchar(64),
    name              varchar(200) NOT NULL,
    exchange          varchar(20)  NOT NULL,
    asset_class       text         NOT NULL,
    status            text         NOT NULL,
    source            text         NOT NULL,
    verified_at       timestamptz  NOT NULL,
    created_at        timestamptz  NOT NULL,
    updated_at        timestamptz  NOT NULL,
    CONSTRAINT instruments_pkey PRIMARY KEY (id),
    CONSTRAINT instruments_symbol_key UNIQUE (symbol),
    CONSTRAINT instruments_provider_asset_unique UNIQUE (source, provider_asset_id),
    CONSTRAINT instruments_symbol_format CHECK (symbol ~ '^[A-Z][A-Z0-9.-]{0,14}$'),
    CONSTRAINT instruments_provider_asset_required CHECK (source = 'demo' OR provider_asset_id IS NOT NULL),
    CONSTRAINT instruments_name_length CHECK (char_length(name) >= 1),
    CONSTRAINT instruments_exchange_length CHECK (char_length(exchange) >= 1),
    CONSTRAINT instruments_asset_class_allowed CHECK (asset_class = 'us_equity'),
    CONSTRAINT instruments_status_allowed CHECK (status IN ('active', 'inactive')),
    CONSTRAINT instruments_source_allowed CHECK (source IN ('alpaca', 'demo'))
);

CREATE TABLE app.transactions (
    id            uuid           NOT NULL,
    portfolio_id  uuid           NOT NULL,
    sequence      bigint         GENERATED ALWAYS AS IDENTITY,
    instrument_id uuid           NOT NULL,
    side          text           NOT NULL,
    quantity      numeric(28,10) NOT NULL,
    price         numeric(28,10) NOT NULL,
    fee           numeric(28,10) NOT NULL,
    executed_at   timestamptz    NOT NULL,
    version       integer        NOT NULL DEFAULT 1,
    created_at    timestamptz    NOT NULL,
    updated_at    timestamptz    NOT NULL,
    CONSTRAINT transactions_pkey PRIMARY KEY (id),
    CONSTRAINT transactions_portfolio_id_fkey FOREIGN KEY (portfolio_id) REFERENCES app.portfolios (id) ON DELETE RESTRICT,
    CONSTRAINT transactions_instrument_id_fkey FOREIGN KEY (instrument_id) REFERENCES app.instruments (id) ON DELETE RESTRICT,
    CONSTRAINT transactions_sequence_key UNIQUE (sequence),
    CONSTRAINT transactions_side_allowed CHECK (side IN ('buy', 'sell')),
    -- '< Infinity' also rejects NaN, which PostgreSQL sorts above every number.
    CONSTRAINT transactions_quantity_valid CHECK (quantity > 0 AND quantity < 'Infinity'::numeric),
    CONSTRAINT transactions_price_valid CHECK (price > 0 AND price < 'Infinity'::numeric),
    CONSTRAINT transactions_fee_valid CHECK (fee >= 0 AND fee < 'Infinity'::numeric),
    CONSTRAINT transactions_executed_at_min CHECK (executed_at >= '1970-01-01T00:00:00Z'::timestamptz),
    CONSTRAINT transactions_version_positive CHECK (version > 0)
);
CREATE INDEX transactions_portfolio_instrument_idx ON app.transactions (portfolio_id, instrument_id, executed_at, sequence);
CREATE INDEX transactions_portfolio_executed_idx ON app.transactions (portfolio_id, executed_at, sequence);

CREATE TABLE app.idempotency_keys (
    portfolio_id uuid         NOT NULL,
    user_id      uuid         NOT NULL,
    operation    text         NOT NULL,
    key          varchar(128) NOT NULL,
    request_hash bytea        NOT NULL,
    result       jsonb        NOT NULL,
    created_at   timestamptz  NOT NULL,
    expires_at   timestamptz  NOT NULL,
    -- portfolio_id first so the FK check on portfolio delete can use this index.
    CONSTRAINT idempotency_keys_pkey PRIMARY KEY (portfolio_id, user_id, operation, key),
    CONSTRAINT idempotency_keys_portfolio_id_fkey FOREIGN KEY (portfolio_id) REFERENCES app.portfolios (id) ON DELETE RESTRICT,
    CONSTRAINT idempotency_keys_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT idempotency_keys_operation_allowed CHECK (operation IN ('transactions.create')),
    CONSTRAINT idempotency_keys_key_length CHECK (char_length(key) >= 1),
    CONSTRAINT idempotency_keys_hash_length CHECK (octet_length(request_hash) = 32),
    CONSTRAINT idempotency_keys_expiry_valid CHECK (expires_at > created_at),
    -- Backstop, not the same limit: Go caps the raw result at 16 KiB, but jsonb::text re-serializes
    -- with extra spaces, so the two counts differ (see audit_events_metadata_size).
    CONSTRAINT idempotency_keys_result_size CHECK (octet_length(result::text) <= 17408)
);
CREATE INDEX idempotency_keys_expires_idx ON app.idempotency_keys (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON app.portfolios, app.transactions TO app_runtime;
GRANT SELECT, INSERT ON app.instruments TO app_runtime;
GRANT SELECT, INSERT, DELETE ON app.idempotency_keys TO app_runtime;

GRANT SELECT (portfolio_id, user_id, operation, key, expires_at), DELETE ON app.idempotency_keys TO app_maintenance;

-- +goose Down
DROP TABLE app.idempotency_keys;
DROP TABLE app.transactions;
DROP TABLE app.instruments;
DROP TABLE app.portfolios;
