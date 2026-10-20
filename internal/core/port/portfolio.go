package port

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
)

// ID arguments are ordered ownerID, portfolioID, transactionID. Every query filters by the
// owner, so another user's portfolio reads as domain.ErrNotFound. A method that reads one row
// returns domain.ErrNotFound when the row does not exist or is not the owner's. Lists are newest
// first with a unique tie-break (portfolios: created_at, id; transactions: executed_at,
// sequence), so consecutive pages never repeat or skip a row.

// PortfolioRepository stores portfolios scoped to their owner.
type PortfolioRepository interface {
	// Insert returns domain.ErrPortfolioNameConflict when the owner already has the name in any
	// letter case.
	Insert(ctx context.Context, portfolio domain.Portfolio) error
	GetOwned(ctx context.Context, ownerID, portfolioID domain.ID) (domain.Portfolio, error)
	// LockOwned takes the portfolio row lock that serializes every change inside the portfolio.
	LockOwned(ctx context.Context, ownerID, portfolioID domain.ID) (domain.Portfolio, error)
	ListOwned(ctx context.Context, ownerID domain.ID, page domain.Page) (domain.PortfolioPage, error)
	// UpdateOwned writes name and description when portfolio.Version still matches and returns
	// the row with version + 1; a mismatch returns domain.ErrVersionConflict and a name the owner
	// already uses returns domain.ErrPortfolioNameConflict.
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
	// Update changes the trade fields of a transaction of transaction.PortfolioID and returns the
	// row with version + 1; a version mismatch returns domain.ErrVersionConflict. It never changes
	// portfolio_id: a trade cannot move to another portfolio.
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
	// Save returns domain.ErrIdempotencyConflict when the scope already has the key. Callers hold
	// the portfolio row lock (LockOwned) and call Get first, so a concurrent Save cannot win. If a
	// conflict is returned anyway, the surrounding transaction is aborted (see UnitOfWork): the
	// caller must read the stored record in a new transaction, not in the same callback.
	Save(ctx context.Context, record domain.IdempotencyRecord) error
	DeleteForPortfolio(ctx context.Context, ownerID, portfolioID domain.ID) error
}
