-- name: GetInstrumentBySymbol :one
SELECT id, symbol, provider_asset_id, name, exchange, asset_class, status, source, verified_at, created_at, updated_at
FROM app.instruments WHERE symbol = $1;

-- ON CONFLICT DO NOTHING covers both the symbol and the (source, provider_asset_id) unique rules,
-- so a concurrent insert of the same instrument keeps the transaction usable.
-- name: InsertInstrument :execrows
INSERT INTO app.instruments (id, symbol, provider_asset_id, name, exchange, asset_class, status, source, verified_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
ON CONFLICT DO NOTHING;
