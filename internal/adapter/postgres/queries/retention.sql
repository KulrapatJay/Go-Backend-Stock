-- Retention cleanup, run by the maintenance role (cmd/maintenance). That role can read only the
-- expiry columns named in the migrations, so every query selects by those and nothing else. Each
-- statement deletes the oldest limit rows first, so a long backlog shrinks in order.

-- name: DeleteAuditEventsBefore :execrows
DELETE FROM app.audit_events
WHERE id IN (
    SELECT a.id FROM app.audit_events a
    WHERE a.occurred_at < @before::timestamptz
    ORDER BY a.occurred_at
    LIMIT @batch_limit::int
);

-- name: DeleteIdempotencyKeysExpiredBefore :execrows
DELETE FROM app.idempotency_keys
WHERE (portfolio_id, user_id, operation, key) IN (
    SELECT k.portfolio_id, k.user_id, k.operation, k.key FROM app.idempotency_keys k
    WHERE k.expires_at < @before::timestamptz
    ORDER BY k.expires_at
    LIMIT @batch_limit::int
);

-- name: DeleteSessionsExpiredBefore :execrows
DELETE FROM app.sessions
WHERE id IN (
    SELECT s.id FROM app.sessions s
    WHERE s.expires_at < @before::timestamptz
    ORDER BY s.expires_at
    LIMIT @batch_limit::int
);

-- name: DeleteOTPChallengesExpiredBefore :execrows
DELETE FROM app.otp_challenges
WHERE id IN (
    SELECT c.id FROM app.otp_challenges c
    WHERE c.expires_at < @before::timestamptz
    ORDER BY c.expires_at
    LIMIT @batch_limit::int
);

-- name: DeleteOIDCFlowsExpiredBefore :execrows
DELETE FROM app.oauth_flows
WHERE id IN (
    SELECT f.id FROM app.oauth_flows f
    WHERE f.expires_at < @before::timestamptz
    ORDER BY f.expires_at
    LIMIT @batch_limit::int
);

-- name: DeleteRateLimitEventsBefore :execrows
DELETE FROM app.rate_limit_events
WHERE id IN (
    SELECT e.id FROM app.rate_limit_events e
    WHERE e.occurred_at < @before::timestamptz
    ORDER BY e.occurred_at
    LIMIT @batch_limit::int
);

-- A key that still has an event cannot go (the foreign key restricts it), so only idle keys are
-- selected. An event inserted after this statement started makes the delete fail with 23503; the
-- repository turns that into "nothing deleted".
-- name: DeleteIdleRateLimitKeys :execrows
DELETE FROM app.rate_limit_keys
WHERE key_hash IN (
    SELECT k.key_hash FROM app.rate_limit_keys k
    WHERE k.expires_at < @before::timestamptz
      AND NOT EXISTS (SELECT 1 FROM app.rate_limit_events e WHERE e.key_hash = k.key_hash)
    ORDER BY k.expires_at
    LIMIT @batch_limit::int
);
