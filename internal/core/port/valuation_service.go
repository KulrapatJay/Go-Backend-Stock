package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// ValuationService is the inbound port for what a portfolio holds and what it is worth. Every method
// needs an authenticated principal (a zero one returns domain.ErrUnauthenticated) and only reaches the
// portfolios of that principal: another user's portfolio looks like a missing one.
type ValuationService interface {
	// Holdings returns one holding per instrument ever traded in the portfolio, sold-out ones included,
	// sorted by symbol. It never calls a price provider.
	Holdings(ctx context.Context, p domain.Principal, portfolioID domain.ID) ([]domain.Holding, error)
	// Value prices the holdings with delayed prices. A provider that fails or has no price leaves the
	// affected positions unpriced (see domain.PortfolioValuation); it is not an error.
	Value(ctx context.Context, p domain.Principal, portfolioID domain.ID) (domain.PortfolioValuation, error)
	// ValueAll prices every portfolio of the principal with the same delayed prices, asked for once,
	// and adds them up (see domain.AccountValuation). A principal without portfolios gets a summary
	// of zeros, not an error.
	ValueAll(ctx context.Context, p domain.Principal) (domain.AccountValuation, error)
}
