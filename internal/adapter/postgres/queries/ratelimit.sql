-- Upserting the key takes its row lock, which serializes concurrent admissions for that key.
-- name: UpsertRateLimitKey :exec
INSERT INTO app.rate_limit_keys (key_hash, scope, expires_at)
VALUES ($1, $2, $3)
ON CONFLICT (key_hash) DO UPDATE SET expires_at = GREATEST(app.rate_limit_keys.expires_at, EXCLUDED.expires_at);

-- name: CountRateLimitEvents :one
SELECT count(*) FROM app.rate_limit_events WHERE key_hash = $1 AND occurred_at > $2;

-- The event that must leave the window before one more attempt fits: the (n+1)-th oldest.
-- name: NthRateLimitEventTime :one
SELECT occurred_at FROM app.rate_limit_events
WHERE key_hash = $1 AND occurred_at > $2
ORDER BY occurred_at, id
OFFSET $3 LIMIT 1;

-- name: InsertRateLimitEvent :exec
INSERT INTO app.rate_limit_events (key_hash, occurred_at) VALUES ($1, $2);

-- Returns a row only for the first denial in a minute per key, so attacks cannot flood audit.
-- name: ClaimRateLimitDenialAudit :execrows
UPDATE app.rate_limit_keys SET last_denial_audit_at = @now
WHERE key_hash = @key_hash AND (last_denial_audit_at IS NULL OR last_denial_audit_at <= @audit_before);
