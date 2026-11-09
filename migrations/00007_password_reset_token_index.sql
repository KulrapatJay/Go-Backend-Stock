-- +goose NO TRANSACTION
-- +goose Up
-- Confirm finds the challenge by the hash of its reset token. otp_challenges already holds rows in a
-- shared database, so the index is built CONCURRENTLY, which cannot run inside a transaction
-- (AGENTS.md: migrations). No IF NOT EXISTS: a failed concurrent build leaves an invalid index
-- that must be dropped by hand, not silently kept. NULLs are left out: only verified challenges have a token.
CREATE UNIQUE INDEX CONCURRENTLY otp_challenges_reset_token_hash_key
    ON app.otp_challenges (reset_token_hash) WHERE reset_token_hash IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS app.otp_challenges_reset_token_hash_key;
