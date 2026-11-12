package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// instrumentVerifier checks with the instrument provider that a symbol the API has not stored yet is a real
// US stock, and builds the instrument to store. It spends the provider quota, so the caller must have
// shown the right to ask (an owned portfolio, or just a login for public market data) before it calls
// verify, and verify itself applies the per-user lookup limit.
type instrumentVerifier struct {
	provider port.InstrumentProvider
	store    port.Repositories
	uow      port.UnitOfWork
	clock    port.Clock
	limits   *RateLimitPolicy
	log      *slog.Logger
}

// verify returns the instrument for symbol, not yet stored: domain.ErrUnknownSymbol when the provider has no
// such US stock, domain.ErrDependencyUnavailable when it could not answer, and a rate_limited error (with its
// security.rate_limited audit event) past the per-user limit. It never holds a lock or a transaction while it
// waits for the provider.
func (v instrumentVerifier) verify(ctx context.Context, p domain.Principal, symbol domain.Symbol, meta domain.RequestMeta) (domain.Instrument, error) {
	rules, err := v.limits.InstrumentLookup(p.UserID)
	if err != nil {
		return domain.Instrument{}, err
	}
	if err := admitRules(ctx, v.store.RateLimits(), v.clock, rules, func(d domain.RateLimitDecision) {
		// A lost marker is logged by commitFailureAudit; the client still gets its 429.
		_ = commitFailureAudit(ctx, v.uow, v.log, meta, domain.EventSecurityRateLimited, func() (domain.AuditEvent, error) {
			return newAuditEvent(v.clock, meta, rateLimitedSpec(p, d), securityMetadata{IP: meta.MaskedIP, UserAgent: meta.UserAgent, Scope: string(d.Scope)})
		})
	}); err != nil {
		return domain.Instrument{}, err
	}

	found, err := v.provider.Lookup(ctx, symbol)
	switch {
	case errors.Is(err, domain.ErrUnknownSymbol):
		return domain.Instrument{}, err
	case err != nil:
		return domain.Instrument{}, fmt.Errorf("%w: %w", domain.ErrDependencyUnavailable, err)
	case found.AssetClass != domain.AssetClassUSEquity:
		return domain.Instrument{}, domain.ErrUnknownSymbol
	case found.Symbol != symbol:
		// The provider answered for another ticker (it followed a rename): phase 1 does not
		// follow symbol changes, so the symbol asked for is not one it knows.
		return domain.Instrument{}, domain.ErrUnknownSymbol
	}
	if found.ID, err = domain.NewID(); err != nil {
		return domain.Instrument{}, err
	}
	now := NowForStorage(v.clock)
	found.VerifiedAt, found.CreatedAt, found.UpdatedAt = now, now, now
	instrument, err := domain.NewInstrument(found)
	if err != nil {
		return domain.Instrument{}, err
	}
	return instrument, nil
}
