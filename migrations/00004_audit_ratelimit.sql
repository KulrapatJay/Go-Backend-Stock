-- +goose Up
CREATE TABLE app.audit_events (
    id            uuid        NOT NULL,
    occurred_at   timestamptz NOT NULL,
    event_type    varchar(80) NOT NULL,
    actor_user_id uuid,
    request_id    uuid        NOT NULL,
    session_id    uuid,
    target_type   varchar(40) NOT NULL,
    target_id     text,
    outcome       text        NOT NULL,
    reason_code   varchar(80),
    metadata      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- No foreign keys: audit outlives the users, sessions and targets it mentions.
    CONSTRAINT audit_events_pkey PRIMARY KEY (id),
    CONSTRAINT audit_events_event_type_length CHECK (char_length(event_type) >= 1),
    CONSTRAINT audit_events_target_type_length CHECK (char_length(target_type) >= 1),
    CONSTRAINT audit_events_outcome_allowed CHECK (outcome IN ('success', 'failure', 'denied')),
    CONSTRAINT audit_events_metadata_object CHECK (jsonb_typeof(metadata) = 'object'),
    -- Go limits raw metadata to 8 KiB; jsonb::text adds spaces, so leave 1 KiB of headroom.
    CONSTRAINT audit_events_metadata_size CHECK (octet_length(metadata::text) <= 9216)
);
CREATE INDEX audit_events_actor_occurred_idx ON app.audit_events (actor_user_id, occurred_at);
CREATE INDEX audit_events_occurred_idx ON app.audit_events (occurred_at);
CREATE INDEX audit_events_request_idx ON app.audit_events (request_id);

CREATE TABLE app.rate_limit_keys (
    key_hash             bytea       NOT NULL,
    scope                text        NOT NULL,
    expires_at           timestamptz NOT NULL,
    last_denial_audit_at timestamptz,
    CONSTRAINT rate_limit_keys_pkey PRIMARY KEY (key_hash),
    CONSTRAINT rate_limit_keys_hash_length CHECK (octet_length(key_hash) = 32),
    CONSTRAINT rate_limit_keys_scope_allowed CHECK (scope IN (
        'login_email', 'login_ip', 'signup_ip', 'google_start_ip',
        'email_verification_request_email', 'email_verification_request_ip', 'email_verification_confirm_ip',
        'password_reset_request_email', 'password_reset_request_ip', 'password_reset_confirm_ip',
        'instrument_lookup_user'))
);
CREATE INDEX rate_limit_keys_expires_idx ON app.rate_limit_keys (expires_at);

CREATE TABLE app.rate_limit_events (
    id          bigint      GENERATED ALWAYS AS IDENTITY,
    key_hash    bytea       NOT NULL,
    occurred_at timestamptz NOT NULL,
    CONSTRAINT rate_limit_events_pkey PRIMARY KEY (id),
    CONSTRAINT rate_limit_events_key_hash_fkey FOREIGN KEY (key_hash) REFERENCES app.rate_limit_keys (key_hash) ON DELETE RESTRICT
);
CREATE INDEX rate_limit_events_key_occurred_idx ON app.rate_limit_events (key_hash, occurred_at);
CREATE INDEX rate_limit_events_occurred_idx ON app.rate_limit_events (occurred_at);

-- Runtime may only append audit events: no SELECT, UPDATE or DELETE (AGENTS.md: append-only).
GRANT INSERT ON app.audit_events TO app_runtime;
GRANT SELECT, INSERT, UPDATE ON app.rate_limit_keys TO app_runtime;
GRANT SELECT, INSERT ON app.rate_limit_events TO app_runtime;

GRANT SELECT (id, occurred_at), DELETE ON app.audit_events TO app_maintenance;
GRANT SELECT (key_hash, expires_at), DELETE ON app.rate_limit_keys TO app_maintenance;
GRANT SELECT (id, key_hash, occurred_at), DELETE ON app.rate_limit_events TO app_maintenance;

-- +goose Down
DROP TABLE app.rate_limit_events;
DROP TABLE app.rate_limit_keys;
DROP TABLE app.audit_events;
