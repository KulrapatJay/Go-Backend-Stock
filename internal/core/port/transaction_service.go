package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// TransactionService is the inbound port of the transaction use cases for the HTTP adapter. Every
// method needs an authenticated principal (a zero one returns domain.ErrUnauthenticated) and only
// reaches portfolios of that principal: another user's portfolio looks like a missing one.
type TransactionService interface {
	// Create records a trade in the portfolio. key is the Idempotency-Key of the request: the same
	// key with the same input returns the first result without writing again, the same key with
	// another input returns domain.ErrIdempotencyConflict.
	Create(ctx context.Context, p domain.Principal, portfolioID domain.ID, key string, in domain.TransactionInput, meta domain.RequestMeta) (domain.Transaction, error)
	// List returns one page of the portfolio's transactions, newest first.
	List(ctx context.Context, p domain.Principal, portfolioID domain.ID, f domain.TransactionFilter) (domain.TransactionPage, error)
	// Get returns one transaction of the portfolio or domain.ErrNotFound.
	Get(ctx context.Context, p domain.Principal, portfolioID, transactionID domain.ID) (domain.Transaction, error)
	// Update replaces the trade fields of a transaction. expected is the version the client last
	// read; a different current version returns domain.ErrVersionConflict and writes nothing. The
	// change is refused when it would leave a position negative at any point of the history.
	Update(ctx context.Context, p domain.Principal, portfolioID, transactionID domain.ID, expected domain.Version, in domain.TransactionInput, meta domain.RequestMeta) (domain.Transaction, error)
	// Delete removes a transaction, with the same version and history checks as Update.
	Delete(ctx context.Context, p domain.Principal, portfolioID, transactionID domain.ID, expected domain.Version, meta domain.RequestMeta) error
}
