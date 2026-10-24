-- Every query filters by user_id, so another user's portfolio reads as not found.

-- ON CONFLICT DO NOTHING keeps the transaction usable: zero rows inserted means the owner already
-- uses the name in some letter case (portfolios_user_name_unique).
-- name: InsertPortfolio :execrows
INSERT INTO app.portfolios (id, user_id, name, description, currency, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, 1, $6, $6)
ON CONFLICT (user_id, lower(name)) DO NOTHING;

-- name: GetOwnedPortfolio :one
SELECT id, user_id, name, description, currency, version, created_at, updated_at
FROM app.portfolios WHERE id = $1 AND user_id = $2;

-- name: LockOwnedPortfolio :one
SELECT id, user_id, name, description, currency, version, created_at, updated_at
FROM app.portfolios WHERE id = $1 AND user_id = $2
FOR UPDATE;

-- Newest first; (created_at, id) is unique, so pages never repeat or skip a row. The first page
-- passes NULL for both after_* values.
-- name: ListOwnedPortfolios :many
SELECT id, user_id, name, description, currency, version, created_at, updated_at
FROM app.portfolios
WHERE user_id = @user_id
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @row_limit;

-- A rename to a name the owner already uses violates portfolios_user_name_unique, which aborts
-- the transaction; the service returns the conflict without writing anything else.
-- name: UpdateOwnedPortfolio :one
UPDATE app.portfolios
SET name = @name, description = @description, version = version + 1, updated_at = @updated_at
WHERE id = @id AND user_id = @user_id AND version = @version
RETURNING id, user_id, name, description, currency, version, created_at, updated_at;

-- name: OwnedPortfolioExists :one
SELECT EXISTS (SELECT 1 FROM app.portfolios WHERE id = $1 AND user_id = $2);

-- Deletes only an empty portfolio. Zero rows means it is missing, not the owner's, or not empty.
-- name: DeleteOwnedEmptyPortfolio :execrows
DELETE FROM app.portfolios p
WHERE p.id = $1 AND p.user_id = $2
  AND NOT EXISTS (SELECT 1 FROM app.transactions t WHERE t.portfolio_id = p.id);
