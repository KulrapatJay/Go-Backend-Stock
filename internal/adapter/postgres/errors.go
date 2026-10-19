package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go-backend-stock/internal/core/domain"
)

const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
)

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
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlStateUniqueViolation:
			if de, ok := uniqueConflicts[pgErr.ConstraintName]; ok {
				return de
			}
		case sqlStateForeignKeyViolation:
			if pgErr.ConstraintName == "transactions_portfolio_id_fkey" {
				return domain.ErrPortfolioNotEmpty
			}
		}
	}
	return fmt.Errorf("%w: %w", domain.ErrInternal, err)
}
