package postgres

import (
	"context"
	"encoding/json"
	"time"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// IdempotencyRepository stores the first result of each idempotent request (24 hours by default,
// set by the service through ExpiresAt).
type IdempotencyRepository struct {
	q *sqlc.Queries
}

var _ port.IdempotencyRepository = (*IdempotencyRepository)(nil)

// Get returns the record for the key, expired or not (the service decides), or domain.ErrNotFound.
func (r *IdempotencyRepository) Get(ctx context.Context, scope domain.IdempotencyScope, key string) (domain.IdempotencyRecord, error) {
	row, err := r.q.GetIdempotencyKey(ctx, sqlc.GetIdempotencyKeyParams{
		PortfolioID: pgUUID(scope.PortfolioID), UserID: pgUUID(scope.UserID), Operation: string(scope.Operation), Key: key,
	})
	if err != nil {
		return domain.IdempotencyRecord{}, mapError(err)
	}
	return idempotencyFromRow(row)
}

// DeleteExpired removes the key's record only if it has expired at now, so an expired key can be
// reused; a live record is left alone.
func (r *IdempotencyRepository) DeleteExpired(ctx context.Context, scope domain.IdempotencyScope, key string, now time.Time) error {
	return mapError(r.q.DeleteExpiredIdempotencyKey(ctx, sqlc.DeleteExpiredIdempotencyKeyParams{
		PortfolioID: pgUUID(scope.PortfolioID), UserID: pgUUID(scope.UserID), Operation: string(scope.Operation), Key: key,
		Now: pgTime(now),
	}))
}

// Save stores a record built with domain.NewIdempotencyRecord under the scope user's own
// portfolio; a missing or foreign portfolio returns domain.ErrNotFound. A key the scope already
// has returns domain.ErrIdempotencyConflict and aborts the transaction (see the port).
func (r *IdempotencyRepository) Save(ctx context.Context, rec domain.IdempotencyRecord) error {
	n, err := r.q.InsertIdempotencyKey(ctx, sqlc.InsertIdempotencyKeyParams{
		PortfolioID: pgUUID(rec.Scope.PortfolioID),
		UserID:      pgUUID(rec.Scope.UserID),
		Operation:   string(rec.Scope.Operation),
		Key:         rec.Key,
		RequestHash: rec.RequestHash,
		Result:      rec.Result,
		CreatedAt:   pgTime(rec.CreatedAt),
		ExpiresAt:   pgTime(rec.ExpiresAt),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteForPortfolio removes every record of the owner's portfolio, before the portfolio is deleted.
func (r *IdempotencyRepository) DeleteForPortfolio(ctx context.Context, ownerID, portfolioID domain.ID) error {
	return mapError(r.q.DeleteIdempotencyKeysForPortfolio(ctx, sqlc.DeleteIdempotencyKeysForPortfolioParams{
		PortfolioID: pgUUID(portfolioID), UserID: pgUUID(ownerID),
	}))
}

func idempotencyFromRow(row sqlc.AppIdempotencyKey) (domain.IdempotencyRecord, error) {
	portfolioID, err := idFromPG("idempotency.portfolio_id", row.PortfolioID)
	if err != nil {
		return domain.IdempotencyRecord{}, err
	}
	userID, err := idFromPG("idempotency.user_id", row.UserID)
	if err != nil {
		return domain.IdempotencyRecord{}, err
	}
	operation, err := domain.ParseIdempotencyOperation(row.Operation)
	if err != nil {
		return domain.IdempotencyRecord{}, fromStored(err)
	}
	return domain.IdempotencyRecord{
		Scope:       domain.IdempotencyScope{UserID: userID, PortfolioID: portfolioID, Operation: operation},
		Key:         row.Key,
		RequestHash: row.RequestHash,
		Result:      json.RawMessage(row.Result),
		CreatedAt:   timeFromPG(row.CreatedAt),
		ExpiresAt:   timeFromPG(row.ExpiresAt),
	}, nil
}
