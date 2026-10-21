-- name: GetIdempotencyKey :one
SELECT portfolio_id, user_id, operation, key, request_hash, result, created_at, expires_at
FROM app.idempotency_keys
WHERE portfolio_id = $1 AND user_id = $2 AND operation = $3 AND key = $4;

-- name: DeleteExpiredIdempotencyKey :exec
DELETE FROM app.idempotency_keys
WHERE portfolio_id = @portfolio_id AND user_id = @user_id AND operation = @operation AND key = @key
  AND expires_at <= @now;

-- Inserts only under a portfolio of the scope's user; zero rows means the portfolio is missing or
-- not theirs. A second Save of the same key still violates idempotency_keys_pkey and aborts the
-- transaction.
-- name: InsertIdempotencyKey :execrows
INSERT INTO app.idempotency_keys (portfolio_id, user_id, operation, key, request_hash, result, created_at, expires_at)
SELECT p.id, p.user_id, @operation::text, @key::text, @request_hash::bytea, @result::jsonb,
       @created_at::timestamptz, @expires_at::timestamptz
FROM app.portfolios p
WHERE p.id = @portfolio_id AND p.user_id = @user_id;

-- name: DeleteIdempotencyKeysForPortfolio :exec
DELETE FROM app.idempotency_keys WHERE portfolio_id = $1 AND user_id = $2;
