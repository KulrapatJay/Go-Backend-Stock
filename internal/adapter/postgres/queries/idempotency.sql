-- name: GetIdempotencyKey :one
SELECT portfolio_id, user_id, operation, key, request_hash, result, created_at, expires_at
FROM app.idempotency_keys
WHERE portfolio_id = $1 AND user_id = $2 AND operation = $3 AND key = $4;

-- name: DeleteExpiredIdempotencyKey :exec
DELETE FROM app.idempotency_keys
WHERE portfolio_id = @portfolio_id AND user_id = @user_id AND operation = @operation AND key = @key
  AND expires_at <= @now;

-- A second Save of the same key violates idempotency_keys_pkey and aborts the transaction.
-- name: InsertIdempotencyKey :exec
INSERT INTO app.idempotency_keys (portfolio_id, user_id, operation, key, request_hash, result, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: DeleteIdempotencyKeysForPortfolio :exec
DELETE FROM app.idempotency_keys WHERE portfolio_id = $1 AND user_id = $2;
