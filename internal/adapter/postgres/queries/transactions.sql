-- Every query joins portfolios on user_id, so another user's transaction reads as not found, and
-- returns the instrument's current symbol. All queries list the same columns in the same order
-- so their rows convert to one Go type (transactionRow).

-- Replay order: executed_at, then the database-assigned sequence.
-- name: OwnedTransactionHistory :many
SELECT t.id, t.portfolio_id, t.sequence, t.instrument_id, i.symbol, t.side, t.quantity, t.price, t.fee,
       t.executed_at, t.version, t.created_at, t.updated_at
FROM app.transactions t
JOIN app.portfolios p ON p.id = t.portfolio_id AND p.user_id = @owner_id
JOIN app.instruments i ON i.id = t.instrument_id
WHERE t.portfolio_id = @portfolio_id
ORDER BY t.executed_at, t.sequence;

-- Newest first by (executed_at, sequence). Filters and the cursor are NULL when unused; from and
-- to are inclusive.
-- name: ListOwnedTransactions :many
SELECT t.id, t.portfolio_id, t.sequence, t.instrument_id, i.symbol, t.side, t.quantity, t.price, t.fee,
       t.executed_at, t.version, t.created_at, t.updated_at
FROM app.transactions t
JOIN app.portfolios p ON p.id = t.portfolio_id AND p.user_id = @owner_id
JOIN app.instruments i ON i.id = t.instrument_id
WHERE t.portfolio_id = @portfolio_id
  AND (sqlc.narg('symbol')::text IS NULL OR i.symbol = sqlc.narg('symbol')::text)
  AND (sqlc.narg('side')::text IS NULL OR t.side = sqlc.narg('side')::text)
  AND (sqlc.narg('from_time')::timestamptz IS NULL OR t.executed_at >= sqlc.narg('from_time')::timestamptz)
  AND (sqlc.narg('to_time')::timestamptz IS NULL OR t.executed_at <= sqlc.narg('to_time')::timestamptz)
  AND (sqlc.narg('after_executed_at')::timestamptz IS NULL
       OR (t.executed_at, t.sequence) < (sqlc.narg('after_executed_at')::timestamptz, sqlc.narg('after_sequence')::bigint))
ORDER BY t.executed_at DESC, t.sequence DESC
LIMIT @row_limit;

-- name: GetOwnedTransaction :one
SELECT t.id, t.portfolio_id, t.sequence, t.instrument_id, i.symbol, t.side, t.quantity, t.price, t.fee,
       t.executed_at, t.version, t.created_at, t.updated_at
FROM app.transactions t
JOIN app.portfolios p ON p.id = t.portfolio_id AND p.user_id = @owner_id
JOIN app.instruments i ON i.id = t.instrument_id
WHERE t.id = @id AND t.portfolio_id = @portfolio_id;

-- Inserts only into the owner's portfolio; no row means the portfolio is missing or not theirs.
-- name: InsertOwnedTransaction :one
WITH ins AS (
    INSERT INTO app.transactions (id, portfolio_id, instrument_id, side, quantity, price, fee, executed_at, version, created_at, updated_at)
    SELECT @id::uuid, p.id, @instrument_id::uuid, @side::text, @quantity::numeric, @price::numeric, @fee::numeric,
           @executed_at::timestamptz, 1, @created_at::timestamptz, @created_at::timestamptz
    FROM app.portfolios p
    WHERE p.id = @portfolio_id AND p.user_id = @owner_id
    RETURNING id, portfolio_id, sequence, instrument_id, side, quantity, price, fee, executed_at, version, created_at, updated_at
)
SELECT ins.id, ins.portfolio_id, ins.sequence, ins.instrument_id, i.symbol, ins.side, ins.quantity, ins.price, ins.fee,
       ins.executed_at, ins.version, ins.created_at, ins.updated_at
FROM ins JOIN app.instruments i ON i.id = ins.instrument_id;

-- portfolio_id is in the WHERE clause and never in SET: a trade cannot move to another portfolio.
-- name: UpdateOwnedTransaction :one
WITH upd AS (
    UPDATE app.transactions t
    SET instrument_id = @instrument_id, side = @side, quantity = @quantity, price = @price, fee = @fee,
        executed_at = @executed_at, version = t.version + 1, updated_at = @updated_at
    FROM app.portfolios p
    WHERE t.id = @id AND t.portfolio_id = @portfolio_id AND t.version = @version
      AND p.id = t.portfolio_id AND p.user_id = @owner_id
    RETURNING t.id, t.portfolio_id, t.sequence, t.instrument_id, t.side, t.quantity, t.price, t.fee,
              t.executed_at, t.version, t.created_at, t.updated_at
)
SELECT upd.id, upd.portfolio_id, upd.sequence, upd.instrument_id, i.symbol, upd.side, upd.quantity, upd.price, upd.fee,
       upd.executed_at, upd.version, upd.created_at, upd.updated_at
FROM upd JOIN app.instruments i ON i.id = upd.instrument_id;

-- name: OwnedTransactionExists :one
SELECT EXISTS (
    SELECT 1 FROM app.transactions t
    JOIN app.portfolios p ON p.id = t.portfolio_id AND p.user_id = @owner_id
    WHERE t.id = @id AND t.portfolio_id = @portfolio_id
);

-- name: DeleteOwnedTransaction :execrows
DELETE FROM app.transactions t
USING app.portfolios p
WHERE t.id = @id AND t.portfolio_id = @portfolio_id AND p.id = t.portfolio_id AND p.user_id = @owner_id;
