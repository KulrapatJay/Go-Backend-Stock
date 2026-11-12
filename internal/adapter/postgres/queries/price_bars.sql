-- name: GetPriceBarCoverage :one
SELECT instrument_id, source, covered_from, covered_through, updated_at
FROM app.price_bar_coverage WHERE instrument_id = $1 AND source = $2;

-- name: ListPriceBars :many
SELECT instrument_id, source, bar_date, open, high, low, close, volume, fetched_at
FROM app.price_bars
WHERE instrument_id = $1 AND source = $2 AND bar_date >= @from_date AND bar_date <= @to_date
ORDER BY bar_date;

-- A bar already stored for the date is kept as it is: a closed day does not change. One batch sends every
-- bar of a save in a single round trip.
-- name: InsertPriceBar :batchexec
INSERT INTO app.price_bars (instrument_id, source, bar_date, open, high, low, close, volume, fetched_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (instrument_id, source, bar_date) DO NOTHING;

-- The coverage only grows when the new range touches or overlaps the stored one. A range that leaves a gap
-- changes nothing, so a gap is never recorded as fetched, even when two first saves race.
-- name: UpsertPriceBarCoverage :exec
INSERT INTO app.price_bar_coverage (instrument_id, source, covered_from, covered_through, updated_at)
VALUES (@instrument_id, @source, @covered_from, @covered_through, @updated_at)
ON CONFLICT (instrument_id, source) DO UPDATE SET
    covered_from    = LEAST(app.price_bar_coverage.covered_from, EXCLUDED.covered_from),
    covered_through = GREATEST(app.price_bar_coverage.covered_through, EXCLUDED.covered_through),
    updated_at      = EXCLUDED.updated_at
WHERE EXCLUDED.covered_from <= app.price_bar_coverage.covered_through + 1
  AND EXCLUDED.covered_through >= app.price_bar_coverage.covered_from - 1;

-- name: ListPriceBarCoverages :many
SELECT instrument_id, source, covered_from, covered_through, updated_at
FROM app.price_bar_coverage
WHERE instrument_id = ANY(@instrument_ids::uuid[]) AND source = @source;

-- name: ListPriceBarsOfInstruments :many
SELECT instrument_id, source, bar_date, open, high, low, close, volume, fetched_at
FROM app.price_bars
WHERE instrument_id = ANY(@instrument_ids::uuid[]) AND source = @source
  AND bar_date >= @from_date AND bar_date <= @to_date
ORDER BY instrument_id, bar_date;
