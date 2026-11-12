package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// ---- fakes: the portfolios and trades of two users, the instruments table and a price provider ----

type perfStore struct {
	port.Repositories
	log         *callLog
	owners      map[domain.ID]domain.ID // portfolio -> user
	hidden      domain.ID               // a portfolio of the caller made after the list was read: trades, but not listed
	trades      []domain.Transaction
	instruments *fakeInstruments
	bars        *fakeBarRepo
	limits      loggingLimits
}

func (s *perfStore) Portfolios() port.PortfolioRepository     { return perfPortfolios{s: s} }
func (s *perfStore) Transactions() port.TransactionRepository { return perfTransactions{s: s} }
func (s *perfStore) Instruments() port.InstrumentRepository   { return perfInstruments{s.instruments} }
func (s *perfStore) PriceBars() port.PriceBarRepository       { return s.bars }
func (s *perfStore) RateLimits() port.RateLimitRepository     { return s.limits }

type perfPortfolios struct {
	port.PortfolioRepository
	s *perfStore
}

func (p perfPortfolios) ListAllOwned(_ context.Context, owner domain.ID) ([]domain.Portfolio, error) {
	p.s.log.add("Portfolios.ListAllOwned")
	var out []domain.Portfolio
	for id, o := range p.s.owners {
		if o == owner && id != p.s.hidden {
			out = append(out, domain.Portfolio{ID: id, UserID: o})
		}
	}
	return out, nil
}

type perfTransactions struct {
	port.TransactionRepository
	s *perfStore
}

func (p perfTransactions) History(_ context.Context, owner, portfolio domain.ID) ([]domain.Transaction, error) {
	p.s.log.add("Transactions.History")
	if p.s.owners[portfolio] != owner {
		return nil, domain.ErrNotFound
	}
	var out []domain.Transaction
	for _, t := range p.s.trades {
		if t.PortfolioID == portfolio {
			out = append(out, t)
		}
	}
	return out, nil
}

func (p perfTransactions) OwnerHistory(_ context.Context, owner domain.ID) ([]domain.Transaction, error) {
	p.s.log.add("Transactions.OwnerHistory")
	var out []domain.Transaction
	for _, t := range p.s.trades {
		if p.s.owners[t.PortfolioID] == owner {
			out = append(out, t)
		}
	}
	return out, nil
}

type perfInstruments struct{ *fakeInstruments }

func (p perfInstruments) ByIDs(_ context.Context, ids []domain.ID) (map[domain.ID]domain.Instrument, error) {
	p.log.add("Instruments.ByIDs")
	out := map[domain.ID]domain.Instrument{}
	for _, in := range p.bySymbol {
		for _, id := range ids {
			if in.ID == id {
				out[id] = in
			}
		}
	}
	return out, nil
}

// perfProvider serves a bar on every weekday of a range for each symbol asked, with a close that a test
// sets per symbol, and records what it was asked.
type perfProvider struct {
	port.PriceProvider
	t      *testing.T
	log    *callLog
	closes map[string]string
	err    error
	asked  []domain.DateRange
	syms   [][]string
}

func (f *perfProvider) DailyBars(ctx context.Context, symbols []domain.Symbol, from, to domain.TradingDate) (domain.DailyBarBatch, error) {
	f.log.add("Prices.DailyBars")
	f.asked = append(f.asked, domain.DateRange{From: from, To: to})
	names := make([]string, len(symbols))
	for i, s := range symbols {
		names[i] = s.String()
	}
	f.syms = append(f.syms, names)
	if err := ctx.Err(); err != nil {
		return domain.DailyBarBatch{}, err
	}
	if f.err != nil {
		return domain.DailyBarBatch{}, f.err
	}
	batch := domain.DailyBarBatch{Bars: map[string][]domain.DailyBar{}, Source: domain.BarSourceAlpaca, Feed: "sip"}
	for _, s := range symbols {
		batch.Bars[s.String()] = weekdayBars(f.t, f.closes[s.String()], from, to)
	}
	return batch, nil
}

func weekdayBars(t *testing.T, closePrice string, from, to domain.TradingDate) []domain.DailyBar {
	t.Helper()
	var out []domain.DailyBar
	for d := from; !d.After(to); d = d.AddDays(1) {
		if wd := d.UTC().Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		p, err := domain.ParsePrice(closePrice)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, domain.DailyBar{Date: d, Open: p, High: p, Low: p, Close: p})
	}
	return out
}

type testPerformance struct {
	svc      *Performance
	log      *callLog
	store    *perfStore
	prices   *perfProvider
	lookup   *fakeInstrumentLookup
	logs     *bytes.Buffer
	caller   domain.Principal
	meta     domain.RequestMeta
	mine     domain.ID // one portfolio of the caller
	other    domain.ID // a portfolio of someone else
	aapl     domain.Instrument
	spy      domain.Instrument
	portfolI domain.ID // a second portfolio of the caller
}

func newTestPerformance(t *testing.T) testPerformance {
	t.Helper()
	log := &callLog{}
	clock := &fakeClock{now: historyNow}
	aapl, spy := mustInstrument(t, "AAPL", clock.Now()), mustInstrument(t, "SPY", clock.Now())
	tp := testPerformance{
		log: log, logs: &bytes.Buffer{}, caller: domain.Principal{UserID: mustID(t), SessionID: mustID(t)}, meta: testMeta(t),
		mine: mustID(t), other: mustID(t), portfolI: mustID(t), aapl: aapl, spy: spy,
	}
	tp.store = &perfStore{
		log:         log,
		owners:      map[domain.ID]domain.ID{tp.mine: tp.caller.UserID, tp.portfolI: tp.caller.UserID, tp.other: mustID(t)},
		instruments: &fakeInstruments{log: log, bySymbol: map[string]domain.Instrument{"AAPL": aapl, "SPY": spy}},
		bars:        newFakeBarRepo(log),
		limits:      loggingLimits{log: log, limits: &fakeRateLimits{decision: domain.RateLimitDecision{Allowed: true}}, scopes: &[]domain.RateLimitScope{}},
	}
	tp.prices = &perfProvider{t: t, log: log, closes: map[string]string{"AAPL": "100", "SPY": "500", "QQQ": "400"}}
	tp.lookup = &fakeInstrumentLookup{log: log, answer: domain.Instrument{
		Name: "Invesco QQQ Trust", Exchange: "NASDAQ", AssetClass: domain.AssetClassUSEquity,
		Status: domain.InstrumentStatusActive, Source: domain.InstrumentSourceAlpaca, ProviderAssetID: "asset-qqq",
	}}
	svc, err := NewPerformance(PerformanceDeps{
		Store: tp.store, UnitOfWork: &fakeUoW{audit: &fakeAudit{}}, Prices: tp.prices, Instruments: tp.lookup,
		Source: domain.BarSourceAlpaca, Clock: clock, Limits: newTestPolicy(t), Logger: slog.New(slog.NewJSONHandler(tp.logs, nil)),
	})
	if err != nil {
		t.Fatalf("NewPerformance() error = %v", err)
	}
	tp.svc = svc
	return tp
}

// buy records a purchase of the instrument at 10:00 New York on the date.
func (tp testPerformance) buy(t *testing.T, portfolio domain.ID, in domain.Instrument, qty, price, date string) {
	t.Helper()
	q, err := domain.ParseQuantity(qty)
	if err != nil {
		t.Fatal(err)
	}
	p, err := domain.ParsePrice(price)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := domain.ParseFee("0")
	d := mustTradingDate(t, date)
	tp.store.trades = append(tp.store.trades, domain.Transaction{
		ID: mustID(t), PortfolioID: portfolio, InstrumentID: in.ID, Symbol: in.Symbol, Side: domain.SideBuy,
		Quantity: q, Price: p, Fee: f, ExecutedAt: d.UTC().Add(15 * time.Hour), Sequence: int64(len(tp.store.trades) + 1),
	})
}

func (tp testPerformance) query(t *testing.T, rng, benchmarks string) port.PerformanceQuery {
	t.Helper()
	r, err := domain.ParsePerformanceRange(rng)
	if err != nil {
		t.Fatal(err)
	}
	b, err := domain.ParseBenchmarks(benchmarks)
	if err != nil {
		t.Fatal(err)
	}
	return port.PerformanceQuery{Range: r, Benchmarks: b}
}

func (tp testPerformance) portfolio(t *testing.T, rng, benchmarks string) (port.PerformanceReport, error) {
	t.Helper()
	return tp.svc.Portfolio(t.Context(), tp.caller, tp.mine, tp.query(t, rng, benchmarks), tp.meta)
}

func (tp testPerformance) askedRanges() []string {
	out := make([]string, len(tp.prices.asked))
	for i, r := range tp.prices.asked {
		out[i] = rangeString(r)
	}
	return out
}

// ---- tests ----

func TestPerformanceNeedsAPrincipal(t *testing.T) {
	tp := newTestPerformance(t)
	_, err := tp.svc.Portfolio(t.Context(), domain.Principal{}, tp.mine, tp.query(t, "1m", ""), tp.meta)
	if !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("Portfolio() error = %v, want unauthenticated", err)
	}
	_, err = tp.svc.Account(t.Context(), domain.Principal{}, tp.query(t, "1m", ""), tp.meta)
	if !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("Account() error = %v, want unauthenticated", err)
	}
	if len(tp.log.calls) != 0 {
		t.Errorf("calls = %v, want nothing touched", tp.log.calls)
	}
}

func TestPerformanceOfAnotherUsersPortfolioIsNotFound(t *testing.T) {
	tp := newTestPerformance(t)
	tp.buy(t, tp.other, tp.aapl, "10", "100", "2026-11-02")
	_, err := tp.svc.Portfolio(t.Context(), tp.caller, tp.other, tp.query(t, "1m", "SPY"), tp.meta)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Portfolio() error = %v, want not found", err)
	}
	if len(tp.prices.asked) != 0 {
		t.Errorf("provider asked %v, want it never asked about someone else's portfolio", tp.askedRanges())
	}
}

func TestPerformanceFillsBarsOfTheRangeAndComputes(t *testing.T) {
	tp := newTestPerformance(t)
	tp.buy(t, tp.mine, tp.aapl, "10", "100", "2026-11-02")
	r, err := tp.portfolio(t, "1m", "SPY")
	if err != nil {
		t.Fatalf("Portfolio() error = %v", err)
	}
	if got := tp.askedRanges(); len(got) != 1 || got[0] != "2026-10-10..2026-11-10" || strings.Join(tp.prices.syms[0], ",") != "AAPL,SPY" {
		t.Fatalf("provider asked %v for %v, want AAPL and SPY for 2026-10-10..2026-11-10", got, tp.prices.syms)
	}
	if r.From.String() != "2026-11-02" || r.To.String() != "2026-11-10" || r.DataThrough.String() != "2026-11-10" ||
		r.Source != domain.BarSourceAlpaca || r.Coverage != domain.CoverageComplete {
		t.Errorf("From %s To %s DataThrough %s Source %s Coverage %s", r.From, r.To, r.DataThrough, r.Source, r.Coverage)
	}
	if len(r.Benchmarks) != 1 || r.Benchmarks[0].Symbol.String() != "SPY" {
		t.Fatalf("benchmarks = %+v", r.Benchmarks)
	}
	if first := r.Points[0]; first.MarketValue == nil || !first.MarketValue.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("first point = %+v, want a value of 10 x 100", first)
	}
}

func TestPerformanceDoesNotAskTheProviderWhenCovered(t *testing.T) {
	tp := newTestPerformance(t)
	tp.buy(t, tp.mine, tp.aapl, "10", "100", "2026-11-02")
	first, err := tp.portfolio(t, "1m", "SPY")
	if err != nil {
		t.Fatal(err)
	}
	calls := len(tp.prices.asked)
	second, err := tp.portfolio(t, "1m", "SPY")
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.prices.asked) != calls {
		t.Errorf("the second request asked the provider again: %v", tp.askedRanges())
	}
	if len(second.Points) != len(first.Points) || second.DataThrough != first.DataThrough {
		t.Errorf("second answer differs: %d points through %s, first had %d through %s", len(second.Points), second.DataThrough, len(first.Points), first.DataThrough)
	}
}

func TestPerformanceNeverAsksForADayThatHasNotClosed(t *testing.T) {
	tp := newTestPerformance(t)
	tp.buy(t, tp.mine, tp.aapl, "10", "100", "2026-11-02")
	if _, err := tp.portfolio(t, "1m", ""); err != nil {
		t.Fatal(err)
	}
	for _, r := range tp.prices.asked {
		if r.To.String() != "2026-11-10" {
			t.Errorf("asked through %s, want 2026-11-10: the 11th is still open in New York", r.To)
		}
	}
}

func seedStored(t *testing.T, tp testPerformance, in domain.Instrument, closePrice, through string) {
	t.Helper()
	from := mustTradingDate(t, "2026-10-01")
	tp.store.bars.seedFor(in.ID, domain.BarSourceAlpaca, from, mustTradingDate(t, through), weekdayBars(t, closePrice, from, mustTradingDate(t, through))...)
}

func TestPerformanceWithStoredBarsSurvivesAProviderThatIsDown(t *testing.T) {
	tp := newTestPerformance(t)
	tp.buy(t, tp.mine, tp.aapl, "10", "100", "2026-11-02")
	seedStored(t, tp, tp.aapl, "100", "2026-11-06")
	seedStored(t, tp, tp.spy, "500", "2026-11-06")
	tp.prices.err = errors.New("provider says: key PKSECRET is not allowed")

	r, err := tp.portfolio(t, "1m", "SPY")
	if err != nil {
		t.Fatalf("Portfolio() error = %v, want the stored bars served", err)
	}
	if r.Coverage != domain.CoveragePartial || r.DataThrough.String() != "2026-11-06" || len(r.Points) == 0 {
		t.Errorf("Coverage %s DataThrough %s points %d, want partial through 2026-11-06", r.Coverage, r.DataThrough, len(r.Points))
	}
	logged := tp.logs.String()
	if strings.Count(logged, `"event":"performance.prices_failed"`) != 1 || !strings.Contains(logged, `"level":"WARN"`) {
		t.Errorf("log = %s, want one warn performance.prices_failed", logged)
	}
	if strings.Contains(logged, "PKSECRET") || strings.Contains(logged, "provider says") {
		t.Errorf("log repeats the text of the provider: %s", logged)
	}
}

func TestPerformanceWithoutStoredBarsIsDependencyUnavailable(t *testing.T) {
	tp := newTestPerformance(t)
	tp.buy(t, tp.mine, tp.aapl, "10", "100", "2026-11-02")
	seedStored(t, tp, tp.spy, "500", "2026-11-06") // the benchmark is stored, the stock never was
	tp.prices.err = errors.New("down")
	_, err := tp.portfolio(t, "1m", "SPY")
	if !errors.Is(err, domain.ErrDependencyUnavailable) {
		t.Fatalf("Portfolio() error = %v, want dependency unavailable", err)
	}
}

func TestPerformanceDoesNotWaitForTheProviderWhenTheRequestHasNoTimeLeft(t *testing.T) {
	tp := newTestPerformance(t)
	tp.buy(t, tp.mine, tp.aapl, "10", "100", "2026-11-02")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second) // less than the 2 seconds kept for the answer
	defer cancel()
	_, err := tp.svc.Portfolio(ctx, tp.caller, tp.mine, tp.query(t, "1m", ""), tp.meta)
	if !errors.Is(err, domain.ErrDependencyUnavailable) || len(tp.prices.asked) != 0 {
		t.Fatalf("Portfolio() error = %v with %d provider calls, want 503 without asking", err, len(tp.prices.asked))
	}
}

func TestPerformanceOfAnAccountAddsPortfoliosAndLeavesOutOtherUsers(t *testing.T) {
	tp := newTestPerformance(t)
	tp.buy(t, tp.mine, tp.aapl, "10", "100", "2026-11-02")
	tp.buy(t, tp.portfolI, tp.aapl, "10", "100", "2026-11-02")
	tp.buy(t, tp.other, tp.aapl, "500", "100", "2026-11-02")
	r, err := tp.svc.Account(t.Context(), tp.caller, tp.query(t, "1m", ""), tp.meta)
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if v := r.Points[0].MarketValue; v == nil || !v.Equal(decimal.NewFromInt(2000)) {
		t.Fatalf("first point = %+v, want 20 shares x 100", r.Points[0])
	}
}

func TestPerformanceWithoutTradesOrBenchmarksAsksNothing(t *testing.T) {
	tp := newTestPerformance(t)
	r, err := tp.portfolio(t, "1m", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.prices.asked) != 0 || r.Points == nil || len(r.Points) != 0 || r.Benchmarks == nil || r.Coverage != domain.CoverageComplete {
		t.Errorf("asked %v, points %v, benchmarks %v, coverage %s; want nothing asked and an empty complete answer", tp.askedRanges(), r.Points, r.Benchmarks, r.Coverage)
	}
	if !r.DataThrough.IsZero() {
		t.Errorf("DataThrough = %s, want zero when nothing was needed", r.DataThrough)
	}
}

func TestPerformanceVerifiesABenchmarkTheStoreHasNotSeen(t *testing.T) {
	tp := newTestPerformance(t)
	tp.buy(t, tp.mine, tp.aapl, "10", "100", "2026-11-02")
	r, err := tp.portfolio(t, "1m", "QQQ")
	if err != nil {
		t.Fatalf("Portfolio() error = %v", err)
	}
	if tp.lookup.calls != 1 || len(tp.store.instruments.upserted) != 1 || tp.store.instruments.upserted[0].Symbol.String() != "QQQ" {
		t.Errorf("lookups %d upserted %v, want QQQ verified once and stored", tp.lookup.calls, tp.store.instruments.upserted)
	}
	if len(r.Benchmarks) != 1 || len(r.Benchmarks[0].Points) == 0 || r.Benchmarks[0].Points[0].ReturnPercent == nil {
		t.Errorf("benchmarks = %+v, want QQQ measured", r.Benchmarks)
	}
	if _, err := tp.portfolio(t, "1m", "QQQ"); err != nil || tp.lookup.calls != 1 {
		t.Errorf("second request: error %v, lookups %d; want the stored QQQ used", err, tp.lookup.calls)
	}
}

func TestPerformanceStopsWhenTheCallerLeft(t *testing.T) {
	for name, stored := range map[string]bool{"nothing stored": false, "bars stored": true} {
		t.Run(name, func(t *testing.T) {
			tp := newTestPerformance(t)
			tp.buy(t, tp.mine, tp.aapl, "10", "100", "2026-11-02")
			if stored {
				seedStored(t, tp, tp.aapl, "100", "2026-11-06") // an answer from the store is possible, but nobody is waiting
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err := tp.svc.Portfolio(ctx, tp.caller, tp.mine, tp.query(t, "1m", ""), tp.meta)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Portfolio() error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestPerformanceOfAnAccountLeavesOutAPortfolioThatIsNotListed(t *testing.T) {
	tp := newTestPerformance(t)
	tp.store.hidden = tp.portfolI
	tp.buy(t, tp.mine, tp.aapl, "10", "100", "2026-11-02")
	tp.buy(t, tp.portfolI, tp.aapl, "90", "100", "2026-11-02")
	r, err := tp.svc.Account(t.Context(), tp.caller, tp.query(t, "1m", ""), tp.meta)
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if v := r.Points[0].MarketValue; v == nil || !v.Equal(decimal.NewFromInt(1000)) {
		t.Fatalf("first point = %+v, want 10 shares x 100: the unlisted portfolio is left out as a whole", r.Points[0])
	}
}
