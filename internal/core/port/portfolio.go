package port

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
)

// ID arguments are ordered ownerID, portfolioID, transactionID. Every query filters by the
// owner, so another user's portfolio reads as domain.ErrNotFound.

// PortfolioRepository stores portfolios scoped to their owner.
type PortfolioRepository interface {
	Insert(ctx context.Context, portfolio domain.Portfolio) error
	GetOwned(ctx context.Context, ownerID, portfolioID domain.ID) (domain.Portfolio, error)
	// LockOwned takes the portfolio row lock that serializes every change inside the portfolio.
	LockOwned(ctx context.Context, ownerID, portfolioID domain.ID) (domain.Portfolio, error)
	ListOwned(ctx context.Context, ownerID domain.ID, page domain.Page) (domain.PortfolioPage, error)
	// UpdateOwned writes name and description when portfolio.Version still matches and returns
	// the row with version + 1; a mismatch returns domain.ErrVersionConflict.
	UpdateOwned(ctx context.Context, portfolio domain.Portfolio, ownerID domain.ID) (domain.Portfolio, error)
	// DeleteOwnedEmpty returns domain.ErrPortfolioNotEmpty while transactions remain.
	DeleteOwnedEmpty(ctx context.Context, ownerID, portfolioID domain.ID) error
}

// TransactionRepository stores trades scoped to their owner and portfolio.
type TransactionRepository interface {
	// History returns the whole portfolio history ordered by (executed_at, sequence) for replay.
	History(ctx context.Context, ownerID, portfolioID domain.ID) ([]domain.Transaction, error)
	List(ctx context.Context, ownerID, portfolioID domain.ID, filter domain.TransactionFilter) (domain.TransactionPage, error)
	Get(ctx context.Context, ownerID, portfolioID, transactionID domain.ID) (domain.Transaction, error)
	// Insert returns the stored row, including the database-assigned sequence.
	Insert(ctx context.Context, ownerID domain.ID, transaction domain.Transaction) (domain.Transaction, error)
	// Update returns the row with version + 1; a version mismatch returns domain.ErrVersionConflict.
	Update(ctx context.Context, ownerID domain.ID, transaction domain.Transaction) (domain.Transaction, error)
	Delete(ctx context.Context, ownerID, portfolioID, transactionID domain.ID) error
}

// InstrumentRepository stores verified instruments shared by all users.
type InstrumentRepository interface {
	BySymbol(ctx context.Context, symbol domain.Symbol) (domain.Instrument, error)
	// Upsert returns the stored row; when a concurrent request inserted the symbol first it
	// returns that existing row.
	Upsert(ctx context.Context, instrument domain.Instrument) (domain.Instrument, error)
}

// IdempotencyRepository stores the first result of each idempotent request.
type IdempotencyRepository interface {
	Get(ctx context.Context, scope domain.IdempotencyScope, key string) (domain.IdempotencyRecord, error)
	DeleteExpired(ctx context.Context, scope domain.IdempotencyScope, key string, now time.Time) error
	Save(ctx context.Context, record domain.IdempotencyRecord) error
	DeleteForPortfolio(ctx context.Context, ownerID, portfolioID domain.ID) error
}
