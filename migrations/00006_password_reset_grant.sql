-- +goose Up
-- Two-step password reset (ADR-0022): POST /auth/password-reset/verify checks the code and stores
-- a one-time reset token on the challenge; confirm then spends the token instead of the code. The
-- challenge stays open (consumed_at NULL) until confirm, so a new code request still invalidates it
-- and, with it, the token. Only hashes are stored. The columns are all NULL or all set, and only on
-- password_reset challenges. Nullable columns without a default change no existing row.
ALTER TABLE app.otp_challenges
    ADD COLUMN verified_at            timestamptz,
    ADD COLUMN reset_token_hash       bytea,
    ADD COLUMN reset_binding_hash     bytea,
    ADD COLUMN reset_token_expires_at timestamptz;

ALTER TABLE app.otp_challenges
    ADD CONSTRAINT otp_challenges_reset_grant_complete CHECK (
        (verified_at IS NULL AND reset_token_hash IS NULL AND reset_binding_hash IS NULL AND reset_token_expires_at IS NULL)
        OR (verified_at IS NOT NULL AND reset_token_hash IS NOT NULL AND reset_binding_hash IS NOT NULL AND reset_token_expires_at IS NOT NULL)),
    ADD CONSTRAINT otp_challenges_reset_grant_purpose CHECK (verified_at IS NULL OR purpose = 'password_reset'),
    ADD CONSTRAINT otp_challenges_reset_grant_expiry CHECK (reset_token_expires_at > verified_at),
    ADD CONSTRAINT otp_challenges_reset_token_hash_length CHECK (octet_length(reset_token_hash) = 32),
    ADD CONSTRAINT otp_challenges_reset_binding_hash_length CHECK (octet_length(reset_binding_hash) = 32);

-- +goose Down
ALTER TABLE app.otp_challenges
    DROP CONSTRAINT otp_challenges_reset_binding_hash_length,
    DROP CONSTRAINT otp_challenges_reset_token_hash_length,
    DROP CONSTRAINT otp_challenges_reset_grant_expiry,
    DROP CONSTRAINT otp_challenges_reset_grant_purpose,
    DROP CONSTRAINT otp_challenges_reset_grant_complete;
ALTER TABLE app.otp_challenges
    DROP COLUMN reset_token_expires_at,
    DROP COLUMN reset_binding_hash,
    DROP COLUMN reset_token_hash,
    DROP COLUMN verified_at;
