package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

const (
	// priceDelay is how far behind now the reference price is: the close of the last bar that ended
	// before now - 15 minutes (AGENTS.md, delayed prices).
	priceDelay = 15 * time.Minute
	// priceTimeout bounds the one call to the price provider (spec section 12).
	priceTimeout = 10 * time.Second
	// priceMargin is the time of the request itself that the provider never gets, so that a slow
	// provider leaves enough to answer with the valuation that has no prices.
	priceMargin = time.Second
)

// ValuationDeps are what Valuation needs; every field is required.
type ValuationDeps struct {
	Store  port.Repositories
	Prices port.PriceProvider
	Clock  port.Clock
	Logger *slog.Logger
}

// Valuation answers what a portfolio holds and what it is worth. It reads the whole history of the
// portfolio in one statement, which is a consistent snapshot, replays it and only then, with no
// transaction and no lock held, asks the price provider once for every symbol still held.
type Valuation struct {
	store  port.Repositories
	prices port.PriceProvider
	clock  port.Clock
	log    *slog.Logger
}

var _ port.ValuationService = (*Valuation)(nil)

// NewValuation checks that every dependency is present.
func NewValuation(d ValuationDeps) (*Valuation, error) {
	if d.Store == nil || d.Prices == nil || d.Clock == nil || d.Logger == nil {
		return nil, errors.New("valuation service is missing a dependency")
	}
	return &Valuation{store: d.Store, prices: d.Prices, clock: d.Clock, log: d.Logger}, nil
}

// Holdings returns the holdings of one of the caller's portfolios. It does not call the price provider.
func (s *Valuation) Holdings(ctx context.Context, p domain.Principal, portfolioID domain.ID) ([]domain.Holding, error) {
	if p.IsZero() {
		return nil, domain.ErrUnauthenticated
	}
	_, holdings, err := s.snapshot(ctx, p, portfolioID)
	return holdings, err
}

// Value prices one of the caller's portfolios. When the provider fails, or has no price for a symbol,
// the positions concerned are unpriced and the rest of the valuation (holdings, realized profit and
// loss) still stands. A request that was cancelled is an error, not a valuation without prices.
func (s *Valuation) Value(ctx context.Context, p domain.Principal, portfolioID domain.ID) (domain.PortfolioValuation, error) {
	if p.IsZero() {
		return domain.PortfolioValuation{}, domain.ErrUnauthenticated
	}
	at, holdings, err := s.snapshot(ctx, p, portfolioID)
	if err != nil {
		return domain.PortfolioValuation{}, err
	}
	var batch domain.QuoteBatch
	failed := false
	if open := domain.OpenSymbols(holdings); len(open) > 0 {
		batch, failed, err = s.quotes(ctx, open, at, portfolioID)
		if err != nil {
			return domain.PortfolioValuation{}, err
		}
	}
	return domain.ValuePortfolio(portfolioID, at, holdings, batch, failed), nil
}

// quotes asks the provider once. The provider gets priceTimeout, or less when the request's own
// deadline is closer, keeping priceMargin for the answer: a provider that is slow or down leaves the
// positions unpriced (failed) and is never an error. Only a request that was cancelled is.
func (s *Valuation) quotes(ctx context.Context, symbols []domain.Symbol, at time.Time, portfolioID domain.ID) (domain.QuoteBatch, bool, error) {
	timeout := priceTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline)-priceMargin)
	}
	var batch domain.QuoteBatch
	var err error
	if timeout > 0 {
		priceCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		batch, err = s.prices.Quotes(priceCtx, symbols, at.Add(-priceDelay))
	} else {
		err = context.DeadlineExceeded // no time left to wait for a provider
	}
	if err == nil {
		return batch, false, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return domain.QuoteBatch{}, false, ctx.Err()
	}
	// The provider's own text may name accounts or keys, so only its kind is logged.
	s.log.Warn("could not get prices, valuing without them",
		slog.String("event", "valuation.prices_failed"),
		slog.String("error_class", errorClass(err)),
		slog.String("portfolio_id", portfolioID.String()),
		slog.Int("symbols", len(symbols)))
	return domain.QuoteBatch{}, true, nil
}

// snapshot reads and replays the history and names the instruments. at is the moment the history was
// read, which is what a valuation reports as calculated_at.
func (s *Valuation) snapshot(ctx context.Context, p domain.Principal, portfolioID domain.ID) (time.Time, []domain.Holding, error) {
	history, err := s.store.Transactions().History(ctx, p.UserID, portfolioID)
	if err != nil {
		return time.Time{}, nil, err
	}
	at := s.clock.Now().UTC()
	positions, err := domain.ReplayHistory(history)
	if err != nil {
		// Stored history that sells more than it holds is damaged data, not a mistake of the caller.
		s.log.Error("stored history cannot be replayed",
			slog.String("event", "valuation.history_invalid"),
			slog.String("portfolio_id", portfolioID.String()))
		return time.Time{}, nil, fmt.Errorf("%w: stored history cannot be replayed", domain.ErrInternal)
	}
	instruments, err := s.store.Instruments().ByIDs(ctx, instrumentIDs(history))
	if err != nil {
		return time.Time{}, nil, err
	}
	names := make(map[domain.ID]string, len(instruments))
	for id, i := range instruments {
		names[id] = i.Name
	}
	return at, domain.BuildHoldings(positions, history, names), nil
}

// instrumentIDs lists each instrument of history once, in order of first appearance.
func instrumentIDs(history []domain.Transaction) []domain.ID {
	seen := make(map[domain.ID]bool)
	var ids []domain.ID
	for _, t := range history {
		if !seen[t.InstrumentID] {
			seen[t.InstrumentID] = true
			ids = append(ids, t.InstrumentID)
		}
	}
	return ids
}

// errorClass names the kind of an error without repeating its text.
func errorClass(err error) string {
	var de *domain.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &de):
		return string(de.Code)
	default:
		return "error"
	}
}
