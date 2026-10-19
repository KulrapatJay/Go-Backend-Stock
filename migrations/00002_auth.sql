-- +goose Up
CREATE TABLE app.users (
    id                uuid         NOT NULL,
    email             varchar(254) NOT NULL,
    password_hash     text,
    email_verified_at timestamptz,
    created_at        timestamptz  NOT NULL,
    updated_at        timestamptz  NOT NULL,
    CONSTRAINT users_pkey PRIMARY KEY (id),
    -- Go normalizes the email (ParseEmail); comparing with lower() here could disagree with Go
    -- for non-ASCII letters, so the stored value is compared exactly.
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_email_length CHECK (char_length(email) BETWEEN 1 AND 254)
);

CREATE TABLE app.auth_providers (
    id               uuid         NOT NULL,
    user_id          uuid         NOT NULL,
    provider         text         NOT NULL,
    provider_subject varchar(255) NOT NULL,
    created_at       timestamptz  NOT NULL,
    CONSTRAINT auth_providers_pkey PRIMARY KEY (id),
    CONSTRAINT auth_providers_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT auth_providers_subject_key UNIQUE (provider, provider_subject),
    CONSTRAINT auth_providers_user_provider_key UNIQUE (user_id, provider),
    CONSTRAINT auth_providers_provider_allowed CHECK (provider = 'google'),
    CONSTRAINT auth_providers_subject_length CHECK (char_length(provider_subject) >= 1)
);

CREATE TABLE app.sessions (
    id            uuid        NOT NULL,
    user_id       uuid        NOT NULL,
    token_hash    bytea       NOT NULL,
    created_at    timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    revoke_reason text,
    CONSTRAINT sessions_pkey PRIMARY KEY (id),
    CONSTRAINT sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT sessions_token_hash_key UNIQUE (token_hash),
    CONSTRAINT sessions_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT sessions_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT sessions_revoke_pair CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL)),
    CONSTRAINT sessions_revoke_reason_allowed CHECK (revoke_reason IN ('logout', 'password_reset'))
);
CREATE INDEX sessions_user_expires_idx ON app.sessions (user_id, expires_at);
CREATE INDEX sessions_expires_idx ON app.sessions (expires_at);

CREATE TABLE app.otp_challenges (
    id              uuid        NOT NULL,
    user_id         uuid        NOT NULL,
    purpose         text        NOT NULL,
    otp_hash        bytea       NOT NULL,
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    failed_attempts smallint    NOT NULL DEFAULT 0,
    consumed_at     timestamptz,
    invalidated_at  timestamptz,
    CONSTRAINT otp_challenges_pkey PRIMARY KEY (id),
    CONSTRAINT otp_challenges_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT otp_challenges_purpose_allowed CHECK (purpose IN ('email_verification', 'password_reset')),
    CONSTRAINT otp_challenges_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT otp_challenges_attempts_valid CHECK (failed_attempts BETWEEN 0 AND 20),
    CONSTRAINT otp_challenges_hash_length CHECK (octet_length(otp_hash) = 32)
);
-- One open challenge per user per flow.
CREATE UNIQUE INDEX otp_challenges_one_open_idx ON app.otp_challenges (user_id, purpose)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;
CREATE INDEX otp_challenges_user_purpose_created_idx ON app.otp_challenges (user_id, purpose, created_at);
CREATE INDEX otp_challenges_expires_idx ON app.otp_challenges (expires_at);

CREATE TABLE app.oauth_flows (
    id                   uuid        NOT NULL,
    purpose              text        NOT NULL,
    user_id              uuid,
    state_hash           bytea       NOT NULL,
    nonce_hash           bytea       NOT NULL,
    browser_binding_hash bytea       NOT NULL,
    created_at           timestamptz NOT NULL,
    expires_at           timestamptz NOT NULL,
    consumed_at          timestamptz,
    CONSTRAINT oauth_flows_pkey PRIMARY KEY (id),
    CONSTRAINT oauth_flows_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT oauth_flows_state_hash_key UNIQUE (state_hash),
    CONSTRAINT oauth_flows_purpose_user_valid CHECK (
        (purpose = 'login' AND user_id IS NULL) OR (purpose = 'link' AND user_id IS NOT NULL)),
    CONSTRAINT oauth_flows_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT oauth_flows_state_hash_length CHECK (octet_length(state_hash) = 32),
    CONSTRAINT oauth_flows_nonce_hash_length CHECK (octet_length(nonce_hash) = 32),
    CONSTRAINT oauth_flows_binding_hash_length CHECK (octet_length(browser_binding_hash) = 32)
);
CREATE INDEX oauth_flows_expires_idx ON app.oauth_flows (expires_at);

-- Runtime: only what the auth repositories (milestone 3b) use. No DELETE: rows expire and are
-- removed by the maintenance role.
GRANT SELECT, INSERT, UPDATE ON app.users TO app_runtime;
GRANT SELECT, INSERT ON app.auth_providers TO app_runtime;
GRANT SELECT, INSERT, UPDATE ON app.sessions, app.otp_challenges, app.oauth_flows TO app_runtime;

-- Maintenance: find expired rows and delete them; nothing else.
GRANT SELECT (id, expires_at), DELETE ON app.sessions, app.otp_challenges, app.oauth_flows TO app_maintenance;

-- +goose Down
DROP TABLE app.oauth_flows;
DROP TABLE app.otp_challenges;
DROP TABLE app.sessions;
DROP TABLE app.auth_providers;
DROP TABLE app.users;
