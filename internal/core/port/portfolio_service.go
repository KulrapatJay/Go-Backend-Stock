package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// PortfolioService is the inbound port of the portfolio use cases for the HTTP adapter. Every method
// needs an authenticated principal (a zero one returns domain.ErrUnauthenticated) and only ever
// reaches the portfolios of that principal: another user's portfolio looks like a missing one.
type PortfolioService interface {
	// Create adds a portfolio named by the caller. A name already used by the caller, ignoring case,
	// returns domain.ErrPortfolioNameConflict.
	Create(ctx context.Context, p domain.Principal, in domain.PortfolioInput, meta domain.RequestMeta) (domain.Portfolio, error)
	// List returns one page of the caller's portfolios, newest first.
	List(ctx context.Context, p domain.Principal, page domain.Page) (domain.PortfolioPage, error)
	// Get returns one of the caller's portfolios or domain.ErrNotFound.
	Get(ctx context.Context, p domain.Principal, id domain.ID) (domain.Portfolio, error)
	// Update renames and re-describes a portfolio. expected is the version the client last read; a
	// different current version returns domain.ErrVersionConflict and writes nothing.
	Update(ctx context.Context, p domain.Principal, id domain.ID, expected domain.Version, in domain.PortfolioInput, meta domain.RequestMeta) (domain.Portfolio, error)
	// Delete removes a portfolio that has no transactions (domain.ErrPortfolioNotEmpty otherwise).
	// expected has the same meaning as in Update.
	Delete(ctx context.Context, p domain.Principal, id domain.ID, expected domain.Version, meta domain.RequestMeta) error
}
