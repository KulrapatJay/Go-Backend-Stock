package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/port"
)

// RetentionRepository deletes expired rows over a connection of the maintenance database role.
// Errors are returned as they come from the driver: this runs in a one-shot job whose caller logs
// them, not behind an HTTP status.
type RetentionRepository struct {
	q *sqlc.Queries
}

var _ port.RetentionRepository = (*RetentionRepository)(nil)

// NewRetentionRepository returns a RetentionRepository on the pool, which must connect as
// app_maintenance. With any other role its deletes fail with SQLSTATE 42501 or find no rights.
func NewRetentionRepository(pool *pgxpool.Pool) *RetentionRepository {
	return &RetentionRepository{q: sqlc.New(pool)}
}

func batchLimit(limit int) (int32, error) {
	if limit < 1 || limit > math.MaxInt32 {
		return 0, fmt.Errorf("retention batch limit must be between 1 and %d, got %d", math.MaxInt32, limit)
	}
	return int32(limit), nil
}

// DeleteAuditEventsBefore deletes up to limit audit events that occurred before the cutoff.
func (r *RetentionRepository) DeleteAuditEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	n, err := batchLimit(limit)
	if err != nil {
		return 0, err
	}
	return r.q.DeleteAuditEventsBefore(ctx, sqlc.DeleteAuditEventsBeforeParams{Before: pgTime(before), BatchLimit: n})
}

// DeleteIdempotencyKeysExpiredBefore deletes up to limit idempotency records that expired before the cutoff.
func (r *RetentionRepository) DeleteIdempotencyKeysExpiredBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	n, err := batchLimit(limit)
	if err != nil {
		return 0, err
	}
	return r.q.DeleteIdempotencyKeysExpiredBefore(ctx, sqlc.DeleteIdempotencyKeysExpiredBeforeParams{Before: pgTime(before), BatchLimit: n})
}

// DeleteSessionsExpiredBefore deletes up to limit sessions that expired before the cutoff.
func (r *RetentionRepository) DeleteSessionsExpiredBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	n, err := batchLimit(limit)
	if err != nil {
		return 0, err
	}
	return r.q.DeleteSessionsExpiredBefore(ctx, sqlc.DeleteSessionsExpiredBeforeParams{Before: pgTime(before), BatchLimit: n})
}

// DeleteOTPChallengesExpiredBefore deletes up to limit OTP challenges that expired before the cutoff.
func (r *RetentionRepository) DeleteOTPChallengesExpiredBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	n, err := batchLimit(limit)
	if err != nil {
		return 0, err
	}
	return r.q.DeleteOTPChallengesExpiredBefore(ctx, sqlc.DeleteOTPChallengesExpiredBeforeParams{Before: pgTime(before), BatchLimit: n})
}

// DeleteOIDCFlowsExpiredBefore deletes up to limit OpenID Connect flows that expired before the cutoff.
func (r *RetentionRepository) DeleteOIDCFlowsExpiredBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	n, err := batchLimit(limit)
	if err != nil {
		return 0, err
	}
	return r.q.DeleteOIDCFlowsExpiredBefore(ctx, sqlc.DeleteOIDCFlowsExpiredBeforeParams{Before: pgTime(before), BatchLimit: n})
}

// DeleteRateLimitEventsBefore deletes up to limit rate-limit events recorded before the cutoff.
func (r *RetentionRepository) DeleteRateLimitEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	n, err := batchLimit(limit)
	if err != nil {
		return 0, err
	}
	return r.q.DeleteRateLimitEventsBefore(ctx, sqlc.DeleteRateLimitEventsBeforeParams{Before: pgTime(before), BatchLimit: n})
}

// DeleteIdleRateLimitKeys deletes up to limit expired rate-limit keys that have no event. If the API
// records an event for one of them while the statement runs, the foreign key refuses the delete
// (SQLSTATE 23503): nothing is deleted this time and the next run takes the key, so this reports
// 0 and no error. The maintenance role cannot lock the row first (it may not UPDATE), so this is
// the only way to meet the race.
func (r *RetentionRepository) DeleteIdleRateLimitKeys(ctx context.Context, before time.Time, limit int) (int64, error) {
	n, err := batchLimit(limit)
	if err != nil {
		return 0, err
	}
	deleted, err := r.q.DeleteIdleRateLimitKeys(ctx, sqlc.DeleteIdleRateLimitKeysParams{Before: pgTime(before), BatchLimit: n})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == sqlStateForeignKeyViolation {
		return 0, nil
	}
	return deleted, err
}
