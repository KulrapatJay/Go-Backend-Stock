-- name: GetSessionByTokenHash :one
SELECT id, user_id, token_hash, created_at, expires_at, revoked_at, revoke_reason
FROM app.sessions WHERE token_hash = $1;

-- name: InsertSession :exec
INSERT INTO app.sessions (id, user_id, token_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: RevokeSession :execrows
UPDATE app.sessions SET revoked_at = $2, revoke_reason = $3
WHERE id = $1 AND revoked_at IS NULL;

-- Only sessions that could still authenticate: not revoked and not expired at $2.
-- name: RevokeUserSessions :execrows
UPDATE app.sessions SET revoked_at = $2, revoke_reason = $3
WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2;
