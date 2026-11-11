package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PriceHistoryDeps are what PriceHistory needs; every field is required. Source is the source of the
// configured price provider (alpaca or demo): the stored bars of that source and no other are read and
// written, so bars made in demo mode are never served as real prices after a switch.
type PriceHistoryDeps struct {
	Store       port.Repositories
	UnitOfWork  port.UnitOfWork
	Prices      port.PriceProvider
	Instruments port.InstrumentProvider
	Source      domain.BarSource
	Clock       port.Clock
	Limits      *RateLimitPolicy
	Logger      *slog.Logger
}

// PriceHistory answers the daily bars of a stock from the stored copy. A day never asked about is first asked
// from the price provider, with no transaction and no lock held, and stored; the days already asked about are
// answered without the provider (ADR-0024).
type PriceHistory struct {
	store    port.Repositories
	prices   port.PriceProvider
	verifier instrumentVerifier
	source   domain.BarSource
	clock    port.Clock
	log      *slog.Logger
}

var _ port.PriceHistoryService = (*PriceHistory)(nil)

// NewPriceHistory checks that every dependency is present and the source is known.
func NewPriceHistory(d PriceHistoryDeps) (*PriceHistory, error) {
	if d.Store == nil || d.UnitOfWork == nil || d.Prices == nil || d.Instruments == nil || d.Clock == nil || d.Limits == nil || d.Logger == nil {
		return nil, errors.New("price history service is missing a dependency")
	}
	if _, err := domain.ParseBarSource(string(d.Source)); err != nil {
		return nil, errors.New("price history service needs the source of the price provider (alpaca or demo)")
	}
	return &PriceHistory{
		store: d.Store, prices: d.Prices, source: d.Source, clock: d.Clock, log: d.Logger,
		verifier: instrumentVerifier{provider: d.Instruments, store: d.Store, uow: d.UnitOfWork, clock: d.Clock, limits: d.Limits, log: d.Logger},
	}, nil
}

// Bars returns the daily bars of symbol dated from..to (see port.PriceHistoryService).
func (s *PriceHistory) Bars(ctx context.Context, p domain.Principal, symbol domain.Symbol, from, to domain.TradingDate, meta domain.RequestMeta) (domain.DailyBarSeries, error) {
	if p.IsZero() {
		return domain.DailyBarSeries{}, domain.ErrUnauthenticated
	}
	if err := checkBarRange(from, to); err != nil {
		return domain.DailyBarSeries{}, err
	}
	instrument, err := s.instrumentFor(ctx, p, symbol, meta)
	if err != nil {
		return domain.DailyBarSeries{}, err
	}

	// A day that has not closed in New York is never fetched or recorded as fetched.
	through := to
	if lastClosed := domain.MarketDate(s.clock.Now()).AddDays(-1); through.After(lastClosed) {
		through = lastClosed
	}
	repo := s.store.PriceBars()
	coverage, covered, err := repo.Coverage(ctx, instrument.ID, s.source)
	if err != nil {
		return domain.DailyBarSeries{}, err
	}

	if !through.Before(from) {
		var known *domain.BarCoverage
		if covered {
			known = &coverage
		}
		fetched, providerErr := false, error(nil)
		for _, missing := range domain.MissingRanges(known, from, through) {
			if providerErr, err = s.fetch(ctx, instrument, missing, meta); err != nil || providerErr != nil {
				break
			}
			fetched = true
		}
		switch {
		case err != nil:
			return domain.DailyBarSeries{}, err
		case providerErr != nil && ctx.Err() != nil:
			return domain.DailyBarSeries{}, ctx.Err() // the caller left: do not answer for them
		case providerErr != nil && !covered:
			return domain.DailyBarSeries{}, fmt.Errorf("%w: %w", domain.ErrDependencyUnavailable, providerErr)
		case providerErr != nil:
			s.log.WarnContext(ctx, "price provider could not answer; serving stored daily bars",
				slog.String("request_id", meta.RequestID.String()), slog.String("symbol", symbol.String()), slog.Any("error", providerErr))
		}
		if fetched {
			if coverage, covered, err = repo.Coverage(ctx, instrument.ID, s.source); err != nil {
				return domain.DailyBarSeries{}, err
			}
		}
	}

	series := domain.DailyBarSeries{
		Symbol: instrument.Symbol, InstrumentName: instrument.Name, Source: s.source, Feed: s.source.Feed(), Bars: []domain.DailyBar{},
	}
	if covered {
		series.DataFrom, series.DataThrough = coverage.From, coverage.Through
	}
	if !through.Before(from) {
		bars, err := repo.Range(ctx, instrument.ID, s.source, from, through)
		if err != nil {
			return domain.DailyBarSeries{}, err
		}
		if len(bars) > 0 {
			series.Bars = bars
		}
	}
	return series, nil
}

// checkBarRange refuses a range a client may not ask for.
func checkBarRange(from, to domain.TradingDate) error {
	switch {
	case from.IsZero() || to.IsZero():
		return domain.NewError(domain.CodeInvalidInput, "from and to are required")
	case to.Before(from):
		return domain.NewError(domain.CodeInvalidInput, "to must not be before from")
	case from.DaysUntil(to)+1 > domain.MaxBarRangeDays:
		return domain.NewError(domain.CodeInvalidInput, fmt.Sprintf("the range must not be longer than %d days", domain.MaxBarRangeDays))
	}
	return nil
}

// instrumentFor returns the stored instrument for symbol. One the API has not seen yet is verified with the
// instrument provider (within the per-user lookup limit) and stored, the way recording a trade does it.
func (s *PriceHistory) instrumentFor(ctx context.Context, p domain.Principal, symbol domain.Symbol, meta domain.RequestMeta) (domain.Instrument, error) {
	stored, err := s.store.Instruments().BySymbol(ctx, symbol)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Instrument{}, err
	}
	found, err := s.verifier.verify(ctx, p, symbol, meta)
	if err != nil {
		return domain.Instrument{}, err
	}
	return s.store.Instruments().Upsert(ctx, found)
}

// fetch asks the price provider for the range and stores what it sends. providerErr is a failure of the
// provider (the caller may serve what is stored); err is a failure of ours (the answer cannot be trusted or
// stored) and always ends the request.
func (s *PriceHistory) fetch(ctx context.Context, instrument domain.Instrument, r domain.DateRange, meta domain.RequestMeta) (providerErr, err error) {
	askCtx, cancel := context.WithTimeout(ctx, priceTimeout)
	defer cancel()
	batch, askErr := s.prices.DailyBars(askCtx, []domain.Symbol{instrument.Symbol}, r.From, r.To)
	if askErr != nil {
		return askErr, nil
	}
	if batch.Source != s.source {
		return nil, fmt.Errorf("%w: the price provider answered as %q but the service is set to %q", domain.ErrInternal, batch.Source, s.source)
	}
	bars := batch.Bars[instrument.Symbol.String()]
	for _, b := range bars {
		if b.Date.Before(r.From) || b.Date.After(r.To) {
			return nil, fmt.Errorf("%w: the price provider sent a bar outside the range asked", domain.ErrInternal)
		}
	}
	return nil, s.store.PriceBars().Save(ctx, instrument.ID, s.source, bars, r, NowForStorage(s.clock))
}
