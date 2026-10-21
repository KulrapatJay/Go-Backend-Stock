package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PortfolioRepository stores portfolios. Every query filters by the owner.
type PortfolioRepository struct {
	q    *sqlc.Queries
	inTx bool
}

var _ port.PortfolioRepository = (*PortfolioRepository)(nil)

// Insert stores a new portfolio at version 1. A name the owner already uses in any letter case
// returns domain.ErrPortfolioNameConflict and leaves the transaction usable.
func (r *PortfolioRepository) Insert(ctx context.Context, p domain.Portfolio) error {
	n, err := r.q.InsertPortfolio(ctx, sqlc.InsertPortfolioParams{
		ID:          pgUUID(p.ID),
		UserID:      pgUUID(p.UserID),
		Name:        p.Name.String(),
		Description: p.Description.String(),
		Currency:    string(p.Currency),
		CreatedAt:   pgTime(p.CreatedAt),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrPortfolioNameConflict
	}
	return nil
}

// GetOwned returns the owner's portfolio, or domain.ErrNotFound.
func (r *PortfolioRepository) GetOwned(ctx context.Context, ownerID, portfolioID domain.ID) (domain.Portfolio, error) {
	row, err := r.q.GetOwnedPortfolio(ctx, sqlc.GetOwnedPortfolioParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return domain.Portfolio{}, mapError(err)
	}
	return portfolioFromRow(row)
}

// LockOwned reads the owner's portfolio with FOR UPDATE; transaction-bound instances only.
func (r *PortfolioRepository) LockOwned(ctx context.Context, ownerID, portfolioID domain.ID) (domain.Portfolio, error) {
	if !r.inTx {
		return domain.Portfolio{}, errLockOutsideTx
	}
	row, err := r.q.LockOwnedPortfolio(ctx, sqlc.LockOwnedPortfolioParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return domain.Portfolio{}, mapError(err)
	}
	return portfolioFromRow(row)
}

// ListOwned returns one page of the owner's portfolios, newest first.
func (r *PortfolioRepository) ListOwned(ctx context.Context, ownerID domain.ID, page domain.Page) (domain.PortfolioPage, error) {
	if page.IsZero() {
		return domain.PortfolioPage{}, domain.NewError(domain.CodeInternal, "portfolio list needs a page")
	}
	params := sqlc.ListOwnedPortfoliosParams{UserID: pgUUID(ownerID), RowLimit: int32(page.Limit() + 1)} //nolint:gosec // Limit is at most domain.MaxPageLimit.
	if page.Cursor() != "" {
		pos, err := decodeCursor(page.Cursor(), cursorKindPortfolios, "")
		if err != nil {
			return domain.PortfolioPage{}, err
		}
		afterID, err := domain.ParseID("cursor", pos.ID)
		if err != nil {
			return domain.PortfolioPage{}, errInvalidCursor
		}
		params.AfterCreatedAt, params.AfterID = pgTime(pos.At), pgUUID(afterID)
	}
	rows, err := r.q.ListOwnedPortfolios(ctx, params)
	if err != nil {
		return domain.PortfolioPage{}, mapError(err)
	}
	var out domain.PortfolioPage
	for i, row := range rows {
		if i == page.Limit() {
			last := out.Items[len(out.Items)-1]
			out.NextCursor = encodeCursor(cursorPosition{Kind: cursorKindPortfolios, At: last.CreatedAt, ID: last.ID.String()})
			break
		}
		p, err := portfolioFromRow(row)
		if err != nil {
			return domain.PortfolioPage{}, err
		}
		out.Items = append(out.Items, p)
	}
	return out, nil
}

// UpdateOwned writes name and description when the version matches and returns the row at
// version + 1. A stale version returns domain.ErrVersionConflict, a missing or foreign portfolio
// domain.ErrNotFound. A name the owner already uses returns domain.ErrPortfolioNameConflict and
// aborts the transaction (see port.UnitOfWork).
func (r *PortfolioRepository) UpdateOwned(ctx context.Context, p domain.Portfolio, ownerID domain.ID) (domain.Portfolio, error) {
	row, err := r.q.UpdateOwnedPortfolio(ctx, sqlc.UpdateOwnedPortfolioParams{
		Name:        p.Name.String(),
		Description: p.Description.String(),
		UpdatedAt:   pgTime(p.UpdatedAt),
		ID:          pgUUID(p.ID),
		UserID:      pgUUID(ownerID),
		Version:     p.Version.Int32(),
	})
	if err = mapError(err); errors.Is(err, domain.ErrNotFound) {
		return domain.Portfolio{}, r.missingOrStale(ctx, ownerID, p.ID)
	} else if err != nil {
		return domain.Portfolio{}, err
	}
	return portfolioFromRow(row)
}

// DeleteOwnedEmpty deletes the owner's portfolio when it has no transactions. Idempotency keys
// must be removed first (IdempotencyRepository.DeleteForPortfolio).
func (r *PortfolioRepository) DeleteOwnedEmpty(ctx context.Context, ownerID, portfolioID domain.ID) error {
	n, err := r.q.DeleteOwnedEmptyPortfolio(ctx, sqlc.DeleteOwnedEmptyPortfolioParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		// A transaction inserted concurrently by a caller that skipped the portfolio lock.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == sqlStateForeignKeyViolation && pgErr.ConstraintName == "transactions_portfolio_id_fkey" {
			return domain.ErrPortfolioNotEmpty
		}
		return mapError(err)
	}
	if n == 1 {
		return nil
	}
	exists, err := r.q.OwnedPortfolioExists(ctx, sqlc.OwnedPortfolioExistsParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return mapError(err)
	}
	if exists {
		return domain.ErrPortfolioNotEmpty
	}
	return domain.ErrNotFound
}

// missingOrStale tells a version conflict from a portfolio that is missing or not the owner's.
func (r *PortfolioRepository) missingOrStale(ctx context.Context, ownerID, portfolioID domain.ID) error {
	exists, err := r.q.OwnedPortfolioExists(ctx, sqlc.OwnedPortfolioExistsParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return mapError(err)
	}
	if exists {
		return domain.ErrVersionConflict
	}
	return domain.ErrNotFound
}

func portfolioFromRow(row sqlc.AppPortfolio) (domain.Portfolio, error) {
	id, err := idFromPG("portfolio.id", row.ID)
	if err != nil {
		return domain.Portfolio{}, err
	}
	userID, err := idFromPG("portfolio.user_id", row.UserID)
	if err != nil {
		return domain.Portfolio{}, err
	}
	name, err := domain.ParsePortfolioName(row.Name)
	if err != nil {
		return domain.Portfolio{}, fromStored(err)
	}
	description, err := domain.ParsePortfolioDescription(row.Description)
	if err != nil {
		return domain.Portfolio{}, fromStored(err)
	}
	currency, err := domain.ParseCurrency(row.Currency)
	if err != nil {
		return domain.Portfolio{}, fromStored(err)
	}
	version, err := domain.ParseVersion(int64(row.Version))
	if err != nil {
		return domain.Portfolio{}, fromStored(err)
	}
	return domain.Portfolio{
		ID: id, UserID: userID, Name: name, Description: description, Currency: currency, Version: version,
		CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}, nil
}
