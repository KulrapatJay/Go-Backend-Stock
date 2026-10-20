-- name: InsertOIDCFlow :exec
INSERT INTO app.oauth_flows (id, purpose, user_id, state_hash, nonce_hash, browser_binding_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- One statement consumes the flow, so two concurrent callbacks cannot both succeed.
-- name: ConsumeOIDCFlow :one
UPDATE app.oauth_flows SET consumed_at = @at
WHERE state_hash = @state_hash AND browser_binding_hash = @browser_binding_hash
  AND consumed_at IS NULL AND expires_at > @at
RETURNING id, purpose, user_id, state_hash, nonce_hash, browser_binding_hash, created_at, expires_at, consumed_at;
