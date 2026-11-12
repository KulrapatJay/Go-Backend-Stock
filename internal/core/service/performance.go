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
	// performanceFillBudget is the longest the provider is given to fill the bars a report lacks.
	performanceFillBudget = 8 * time.Second
	// performanceFillMargin is what is kept of the request's own deadline for reading and answering.
	performanceFillMargin = 2 * time.Second
)

// PerformanceDeps are what Performance needs; every field is required. Source is the source of the
// configured price provider: only the stored bars of that source are read or written.
type PerformanceDeps struct {
	Store       port.Repositories
	UnitOfWork  port.UnitOfWork
	Prices      port.PriceProvider
	Instruments port.InstrumentProvider
	Source      domain.BarSource
	Clock       port.Clock
	Limits      *RateLimitPolicy
	Logger      *slog.Logger
}

// Performance reads the trades of a portfolio or an account, fills the daily bars they need from the price
// provider (no transaction and no lock held), and computes the daily value and return with
// domain.ComputePerformance (ADR-0025). Nothing it computes is stored.
type Performance struct {
	store    port.Repositories
	filler   barFiller
	verifier instrumentVerifier
	source   domain.BarSource
	clock    port.Clock
	log      *slog.Logger
}

var _ port.PerformanceService = (*Performance)(nil)

// NewPerformance checks that every dependency is present and the source is known.
func NewPerformance(d PerformanceDeps) (*Performance, error) {
	if d.Store == nil || d.UnitOfWork == nil || d.Prices == nil || d.Instruments == nil || d.Clock == nil || d.Limits == nil || d.Logger == nil {
		return nil, errors.New("performance service is missing a dependency")
	}
	if _, err := domain.ParseBarSource(string(d.Source)); err != nil {
		return nil, errors.New("performance service needs the source of the price provider (alpaca or demo)")
	}
	return &Performance{
		store: d.Store, source: d.Source, clock: d.Clock, log: d.Logger,
		filler:   barFiller{store: d.Store, prices: d.Prices, source: d.Source, clock: d.Clock},
		verifier: instrumentVerifier{provider: d.Instruments, store: d.Store, uow: d.UnitOfWork, clock: d.Clock, limits: d.Limits, log: d.Logger},
	}, nil
}

// Portfolio reports one of the caller's portfolios (see port.PerformanceService).
func (s *Performance) Portfolio(ctx context.Context, p domain.Principal, portfolioID domain.ID, q port.PerformanceQuery, meta domain.RequestMeta) (port.PerformanceReport, error) {
	if p.IsZero() {
		return port.PerformanceReport{}, domain.ErrUnauthenticated
	}
	trades, err := s.store.Transactions().History(ctx, p.UserID, portfolioID)
	if err != nil {
		return port.PerformanceReport{}, err
	}
	return s.report(ctx, p, trades, q, meta)
}

// Account reports every portfolio of the caller added together (see port.PerformanceService).
func (s *Performance) Account(ctx context.Context, p domain.Principal, q port.PerformanceQuery, meta domain.RequestMeta) (port.PerformanceReport, error) {
	if p.IsZero() {
		return port.PerformanceReport{}, domain.ErrUnauthenticated
	}
	portfolios, err := s.store.Portfolios().ListAllOwned(ctx, p.UserID)
	if err != nil {
		return port.PerformanceReport{}, err
	}
	history, err := s.store.Transactions().OwnerHistory(ctx, p.UserID)
	if err != nil {
		return port.PerformanceReport{}, err
	}
	// A portfolio created after the list was read may already have trades here; it is left out as a whole,
	// like in Valuation.ValueAll.
	listed := make(map[domain.ID]bool, len(portfolios))
	for _, portfolio := range portfolios {
		listed[portfolio.ID] = true
	}
	trades := make([]domain.Transaction, 0, len(history))
	for _, t := range history {
		if listed[t.PortfolioID] {
			trades = append(trades, t)
		}
	}
	return s.report(ctx, p, trades, q, meta)
}

func (s *Performance) report(ctx context.Context, p domain.Principal, history []domain.Transaction, q port.PerformanceQuery, meta domain.RequestMeta) (port.PerformanceReport, error) {
	through := domain.MarketDate(s.clock.Now()).AddDays(-1) // the last day that has closed in New York
	trades := make([]domain.Transaction, 0, len(history))
	var firstTrade domain.TradingDate
	for _, t := range history {
		day := domain.MarketDate(t.ExecutedAt)
		if day.After(through) {
			continue // a day that has not closed has no bar to value it with
		}
		if firstTrade.IsZero() || day.Before(firstTrade) {
			firstTrade = day
		}
		trades = append(trades, t)
	}
	start := q.Range.Start(through, firstTrade)
	if start.After(through) {
		start = through
	}

	held, err := s.heldInstruments(ctx, trades, start)
	if err != nil {
		return port.PerformanceReport{}, err
	}
	benchmarks, err := s.benchmarkInstruments(ctx, p, q.Benchmarks, meta)
	if err != nil {
		return port.PerformanceReport{}, err
	}
	needed := append([]domain.Instrument(nil), held...)
	for _, b := range benchmarks {
		if !containsInstrument(needed, b.ID) {
			needed = append(needed, b)
		}
	}
	if len(needed) == 0 {
		return s.compute(ctx, nil, trades, q, start, through, nil), nil
	}

	providerErr, err := s.fill(ctx, needed, domain.DateRange{From: start, To: through})
	if err != nil {
		return port.PerformanceReport{}, err
	}
	if providerErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return port.PerformanceReport{}, ctx.Err() // the caller left: do not answer for them
		}
		// The provider's own text may name accounts or keys, so only its kind is logged.
		s.log.WarnContext(ctx, "price provider could not fill the daily bars; serving the stored ones",
			slog.String("event", "performance.prices_failed"),
			slog.String("error_class", errorClass(providerErr)),
			slog.String("request_id", meta.RequestID.String()),
			slog.Int("symbols", len(needed)))
	}

	ids := make([]domain.ID, len(needed))
	for i, in := range needed {
		ids[i] = in.ID
	}
	covered, err := s.store.PriceBars().Coverages(ctx, ids, s.source)
	if err != nil {
		return port.PerformanceReport{}, err
	}
	dataThrough := through
	for _, id := range ids {
		c, ok := covered[id]
		if !ok {
			if providerErr != nil {
				return port.PerformanceReport{}, fmt.Errorf("%w: %w", domain.ErrDependencyUnavailable, providerErr)
			}
			dataThrough = domain.TradingDate{}
			continue
		}
		if !dataThrough.IsZero() && c.Through.Before(dataThrough) {
			dataThrough = c.Through
		}
	}
	bars, err := s.store.PriceBars().RangeMany(ctx, ids, s.source, start, through)
	if err != nil {
		return port.PerformanceReport{}, err
	}
	report := s.compute(ctx, bars, trades, q, start, through, benchmarks)
	report.DataThrough = dataThrough
	if providerErr != nil && report.Coverage == domain.CoverageComplete {
		report.Coverage = domain.CoveragePartial
	}
	return report, nil
}

// compute turns trades and stored bars into a report. bars are keyed by instrument; benchmarks are the
// instruments of q.Benchmarks in order.
func (s *Performance) compute(_ context.Context, bars map[domain.ID][]domain.DailyBar, trades []domain.Transaction, q port.PerformanceQuery, start, through domain.TradingDate, benchmarks []domain.Instrument) port.PerformanceReport {
	in := domain.PerformanceInput{Trades: trades, Bars: bars, Start: start, Through: through}
	for i, b := range q.Benchmarks {
		in.Benchmarks = append(in.Benchmarks, domain.BenchmarkBars{Benchmark: b, Bars: bars[benchmarks[i].ID]})
	}
	return port.PerformanceReport{Performance: domain.ComputePerformance(in), Source: s.source}
}

// heldInstruments reads the instruments whose bars the report needs from start on.
func (s *Performance) heldInstruments(ctx context.Context, trades []domain.Transaction, start domain.TradingDate) ([]domain.Instrument, error) {
	ids := domain.InstrumentsHeldSince(trades, start)
	if len(ids) == 0 {
		return nil, nil
	}
	stored, err := s.store.Instruments().ByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Instrument, 0, len(ids))
	for _, id := range ids {
		in, ok := stored[id]
		if !ok {
			return nil, fmt.Errorf("%w: a stored trade refers to an instrument that does not exist", domain.ErrInternal)
		}
		out = append(out, in)
	}
	return out, nil
}

// benchmarkInstruments returns the instrument of each benchmark in order. One the API has not seen yet is
// verified with the instrument provider (within the per-user lookup limit) and stored, like PriceHistory does.
func (s *Performance) benchmarkInstruments(ctx context.Context, p domain.Principal, benchmarks []domain.Benchmark, meta domain.RequestMeta) ([]domain.Instrument, error) {
	out := make([]domain.Instrument, 0, len(benchmarks))
	for _, b := range benchmarks {
		in, err := s.store.Instruments().BySymbol(ctx, b.Symbol)
		if errors.Is(err, domain.ErrNotFound) {
			found, verr := s.verifier.verify(ctx, p, b.Symbol, meta)
			if verr != nil {
				return nil, verr
			}
			in, err = s.store.Instruments().Upsert(ctx, found)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
}

// fill gives the provider performanceFillBudget, or less when the request's own deadline is closer, keeping
// performanceFillMargin for the answer. With no time left the provider is not asked and the result is a
// provider error, so stored bars may still be served.
func (s *Performance) fill(ctx context.Context, instruments []domain.Instrument, r domain.DateRange) (providerErr, err error) {
	budget := performanceFillBudget
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, time.Until(deadline)-performanceFillMargin)
	}
	if budget <= 0 {
		return context.DeadlineExceeded, nil
	}
	fillCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return s.filler.fill(fillCtx, instruments, r)
}

func containsInstrument(list []domain.Instrument, id domain.ID) bool {
	for _, in := range list {
		if in.ID == id {
			return true
		}
	}
	return false
}
