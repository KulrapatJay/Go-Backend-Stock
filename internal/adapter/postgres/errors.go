package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go-backend-stock/internal/core/domain"
)

const (
	sqlStateUniqueViolation = "23505"
	// sqlStateForeignKeyViolation is mapped only where its meaning is known: deleting a portfolio
	// that still has trades (PortfolioRepository.DeleteOwnedEmpty). Elsewhere it is internal; an
	// insert into a missing portfolio, for example, is not "portfolio not empty".
	sqlStateForeignKeyViolation = "23503"

	// Contention and timeouts. The statement did nothing, so the client may send the request again.
	sqlStateSerializationFailure = "40001"
	sqlStateDeadlockDetected     = "40P01"
	sqlStateLockNotAvailable     = "55P03" // lock_timeout
	sqlStateQueryCanceled        = "57014" // statement_timeout
)

// errDatabaseBusy is what a lock wait, deadlock, serialization failure or timeout becomes: the
// request did nothing and can be sent again at once (ADR-0016).
var errDatabaseBusy = domain.NewRetryError(domain.CodeServerBusy, "the database is busy, try again shortly", time.Second)

// uniqueConflicts maps unique constraints to the domain errors the core expects. Constraint names
// are set explicitly in migrations/, so renaming one there must update this map.
var uniqueConflicts = map[string]*domain.Error{
	"portfolios_user_name_unique":      domain.ErrPortfolioNameConflict,
	"auth_providers_subject_key":       domain.ErrIdentityConflict,
	"auth_providers_user_provider_key": domain.ErrIdentityConflict,
	// Two signups for one email at the same time; the service answers as for an existing email.
	"users_email_key":       domain.ErrIdentityConflict,
	"idempotency_keys_pkey": domain.ErrIdempotencyConflict,
}

// mapError converts a database error into a domain error. Anything unexpected becomes internal
// while keeping the original error in the chain for server-side logs; the domain message never
// contains SQL or row values.
//
// Mapping a unique violation to a domain conflict does not recover the transaction: PostgreSQL
// has already aborted it, so the caller must return the error and let withinTx roll back (see
// port.UnitOfWork). Repositories that expect a conflict use ON CONFLICT DO NOTHING instead.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	// The request ran out of time. context.Canceled (the client left) stays internal: nobody is
	// left to retry.
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", errDatabaseBusy, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlStateDeadlockDetected, sqlStateSerializationFailure, sqlStateLockNotAvailable, sqlStateQueryCanceled:
			return fmt.Errorf("%w: %w", errDatabaseBusy, err)
		case sqlStateUniqueViolation:
			if de, ok := uniqueConflicts[pgErr.ConstraintName]; ok {
				return de
			}
		}
	}
	return fmt.Errorf("%w: %w", domain.ErrInternal, err)
}
