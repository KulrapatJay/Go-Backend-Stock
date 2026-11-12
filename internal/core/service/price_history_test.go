package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// The fake price history store keeps bars and coverage in memory and records every call with its source, so
// a test can check what was read and written and for which provider.

type fakeBarRepo struct {
	log       *callLog
	defaultID domain.ID                     // the instrument seedCoverage seeds
	bars      map[string]domain.DailyBar    // key: source/instrument/date
	coverage  map[string]domain.BarCoverage // key: source/instrument
	saves     []savedBars
	sources   []domain.BarSource
	saveErr   error
	readErr   error
}

type savedBars struct {
	instrument domain.ID
	bars       []domain.DailyBar
	fetched    domain.DateRange
	at         time.Time
}

func newFakeBarRepo(log *callLog) *fakeBarRepo {
	return &fakeBarRepo{log: log, bars: map[string]domain.DailyBar{}, coverage: map[string]domain.BarCoverage{}}
}

func (f *fakeBarRepo) key(id domain.ID, source domain.BarSource) string {
	return string(source) + "/" + id.String()
}

func (f *fakeBarRepo) Coverage(_ context.Context, id domain.ID, source domain.BarSource) (domain.BarCoverage, bool, error) {
	f.log.add("PriceBars.Coverage")
	f.sources = append(f.sources, source)
	if f.readErr != nil {
		return domain.BarCoverage{}, false, f.readErr
	}
	c, ok := f.coverage[f.key(id, source)]
	return c, ok, nil
}

func (f *fakeBarRepo) Range(_ context.Context, id domain.ID, source domain.BarSource, from, to domain.TradingDate) ([]domain.DailyBar, error) {
	f.log.add("PriceBars.Range")
	f.sources = append(f.sources, source)
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.barsOf(id, source, from, to), nil
}

func (f *fakeBarRepo) barsOf(id domain.ID, source domain.BarSource, from, to domain.TradingDate) []domain.DailyBar {
	var out []domain.DailyBar
	for k, b := range f.bars {
		if strings.HasPrefix(k, f.key(id, source)+"/") && !b.Date.Before(from) && !b.Date.After(to) {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b domain.DailyBar) int { return b.Date.DaysUntil(a.Date) })
	return out
}

func (f *fakeBarRepo) Coverages(_ context.Context, ids []domain.ID, source domain.BarSource) (map[domain.ID]domain.BarCoverage, error) {
	f.log.add("PriceBars.Coverages")
	f.sources = append(f.sources, source)
	if f.readErr != nil {
		return nil, f.readErr
	}
	out := map[domain.ID]domain.BarCoverage{}
	for _, id := range ids {
		if c, ok := f.coverage[f.key(id, source)]; ok {
			out[id] = c
		}
	}
	return out, nil
}

func (f *fakeBarRepo) RangeMany(_ context.Context, ids []domain.ID, source domain.BarSource, from, to domain.TradingDate) (map[domain.ID][]domain.DailyBar, error) {
	f.log.add("PriceBars.RangeMany")
	f.sources = append(f.sources, source)
	if f.readErr != nil {
		return nil, f.readErr
	}
	out := map[domain.ID][]domain.DailyBar{}
	for _, id := range ids {
		if bars := f.barsOf(id, source, from, to); len(bars) > 0 {
			out[id] = bars
		}
	}
	return out, nil
}

func (f *fakeBarRepo) Save(_ context.Context, id domain.ID, source domain.BarSource, bars []domain.DailyBar, fetched domain.DateRange, now time.Time) error {
	f.log.add("PriceBars.Save")
	f.sources = append(f.sources, source)
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves = append(f.saves, savedBars{instrument: id, bars: bars, fetched: fetched, at: now})
	for _, b := range bars {
		f.bars[f.key(id, source)+"/"+b.Date.String()] = b
	}
	if c, had := f.coverage[f.key(id, source)]; had {
		f.coverage[f.key(id, source)] = c.ExtendedBy(fetched.From, fetched.To)
	} else {
		f.coverage[f.key(id, source)] = domain.BarCoverage{From: fetched.From, Through: fetched.To}
	}
	return nil
}

// seedCoverage marks from..through as already fetched for the source, with the given bars stored, for the
// default instrument.
func (f *fakeBarRepo) seedCoverage(source domain.BarSource, from, through domain.TradingDate, bars ...domain.DailyBar) {
	f.seedFor(f.defaultID, source, from, through, bars...)
}

func (f *fakeBarRepo) seedFor(id domain.ID, source domain.BarSource, from, through domain.TradingDate, bars ...domain.DailyBar) {
	f.coverage[f.key(id, source)] = domain.BarCoverage{From: from, Through: through}
	for _, b := range bars {
		f.bars[f.key(id, source)+"/"+b.Date.String()] = b
	}
}

// fakeBarsProvider is the port.PriceProvider for daily bars: it serves what a test configured and records
// every range it was asked for.
type fakeBarsProvider struct {
	port.PriceProvider
	log    *callLog
	asked  []domain.DateRange
	syms   [][]domain.Symbol
	source domain.BarSource
	serve  func(r domain.DateRange) ([]domain.DailyBar, error)
}

func (f *fakeBarsProvider) DailyBars(ctx context.Context, symbols []domain.Symbol, from, to domain.TradingDate) (domain.DailyBarBatch, error) {
	f.log.add("Prices.DailyBars")
	f.asked = append(f.asked, domain.DateRange{From: from, To: to})
	f.syms = append(f.syms, symbols)
	if err := ctx.Err(); err != nil {
		return domain.DailyBarBatch{}, err
	}
	bars, err := f.serve(domain.DateRange{From: from, To: to})
	if err != nil {
		return domain.DailyBarBatch{}, err
	}
	batch := domain.DailyBarBatch{Bars: map[string][]domain.DailyBar{}, Source: f.source, Feed: f.source.Feed()}
	for _, sym := range symbols {
		batch.Bars[sym.String()] = bars
	}
	return batch, nil
}

type fakeInstrumentLookup struct {
	log    *callLog
	answer domain.Instrument
	err    error
	calls  int
}

func (f *fakeInstrumentLookup) Lookup(_ context.Context, s domain.Symbol) (domain.Instrument, error) {
	f.log.add("Provider.Lookup")
	f.calls++
	if f.err != nil {
		return domain.Instrument{}, f.err
	}
	a := f.answer
	a.Symbol = s
	return a, nil
}

type historyStore struct {
	port.Repositories
	instruments *fakeInstruments
	bars        *fakeBarRepo
	limits      loggingLimits
}

func (s historyStore) Instruments() port.InstrumentRepository { return s.instruments }
func (s historyStore) PriceBars() port.PriceBarRepository     { return s.bars }
func (s historyStore) RateLimits() port.RateLimitRepository   { return s.limits }

type testHistory struct {
	svc      *PriceHistory
	log      *callLog
	repo     *fakeBarRepo
	prices   *fakeBarsProvider
	lookup   *fakeInstrumentLookup
	insts    *fakeInstruments
	limits   *fakeRateLimits
	scopes   *[]domain.RateLimitScope
	clock    *fakeClock
	logs     *bytes.Buffer
	caller   domain.Principal
	meta     domain.RequestMeta
	aapl     domain.Instrument
	provided map[string]domain.DailyBar // what the provider holds, by date
}

// historyNow is Wednesday 11 November 2026, 10:00 in New York: the last closed day is Tuesday 10 November.
var historyNow = time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)

func newTestHistory(t *testing.T) testHistory {
	t.Helper()
	log := &callLog{}
	clock := &fakeClock{now: historyNow}
	aapl := mustInstrument(t, "AAPL", clock.Now())
	insts := &fakeInstruments{log: log, bySymbol: map[string]domain.Instrument{"AAPL": aapl}}
	repo := newFakeBarRepo(log)
	repo.defaultID = aapl.ID
	limits := &fakeRateLimits{decision: domain.RateLimitDecision{Allowed: true}}
	scopes := &[]domain.RateLimitScope{}
	th := testHistory{
		log: log, repo: repo, insts: insts, limits: limits, scopes: scopes, clock: clock, logs: &bytes.Buffer{},
		caller: domain.Principal{UserID: mustID(t), SessionID: mustID(t)}, meta: testMeta(t), aapl: aapl,
		provided: map[string]domain.DailyBar{},
	}
	th.prices = &fakeBarsProvider{log: log, source: domain.BarSourceAlpaca}
	th.prices.serve = func(r domain.DateRange) ([]domain.DailyBar, error) {
		var out []domain.DailyBar
		for d := r.From; !d.After(r.To); d = d.AddDays(1) {
			if b, ok := th.provided[d.String()]; ok {
				out = append(out, b)
			}
		}
		return out, nil
	}
	th.lookup = &fakeInstrumentLookup{log: log, answer: domain.Instrument{
		Name: "Microsoft Corporation", Exchange: "NASDAQ", AssetClass: domain.AssetClassUSEquity,
		Status: domain.InstrumentStatusActive, Source: domain.InstrumentSourceAlpaca, ProviderAssetID: "asset-msft",
	}}
	svc, err := NewPriceHistory(PriceHistoryDeps{
		Store:      historyStore{instruments: insts, bars: repo, limits: loggingLimits{log: log, limits: limits, scopes: scopes}},
		UnitOfWork: &fakeUoW{audit: &fakeAudit{}}, Prices: th.prices, Instruments: th.lookup, Source: domain.BarSourceAlpaca,
		Clock: clock, Limits: newTestPolicy(t), Logger: slog.New(slog.NewJSONHandler(th.logs, nil)),
	})
	if err != nil {
		t.Fatalf("NewPriceHistory() error = %v", err)
	}
	th.svc = svc
	return th
}

func (th testHistory) bars(t *testing.T, symbol, from, to string) (domain.DailyBarSeries, error) {
	t.Helper()
	return th.svc.Bars(t.Context(), th.caller, mustSymbol(t, symbol), mustTradingDate(t, from), mustTradingDate(t, to), th.meta)
}

func mustTradingDate(t *testing.T, s string) domain.TradingDate {
	t.Helper()
	d, err := domain.ParseTradingDate("date", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func serviceBar(t *testing.T, date string) domain.DailyBar {
	t.Helper()
	price := func(s string) domain.Price {
		p, err := domain.ParsePrice(s)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	v, err := domain.ParseVolume("1000")
	if err != nil {
		t.Fatal(err)
	}
	return domain.DailyBar{Date: mustTradingDate(t, date), Open: price("10"), High: price("12"), Low: price("9"), Close: price("11"), Volume: v}
}

// provide makes the provider hold a bar for each date.
func (th testHistory) provide(t *testing.T, dates ...string) {
	t.Helper()
	for _, d := range dates {
		th.provided[d] = serviceBar(t, d)
	}
}

func askedRanges(th testHistory) []string {
	out := make([]string, len(th.prices.asked))
	for i, r := range th.prices.asked {
		out[i] = rangeString(r)
	}
	return out
}

func datesOf(bars []domain.DailyBar) []string {
	out := make([]string, len(bars))
	for i, b := range bars {
		out[i] = b.Date.String()
	}
	return out
}

func TestBarsRefusesAnUnauthenticatedCaller(t *testing.T) {
	th := newTestHistory(t)
	_, err := th.svc.Bars(t.Context(), domain.Principal{}, mustSymbol(t, "AAPL"), mustTradingDate(t, "2026-11-02"), mustTradingDate(t, "2026-11-06"), th.meta)
	if !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("Bars() error = %v, want unauthenticated", err)
	}
	if len(th.log.calls) != 0 {
		t.Errorf("calls = %v, want nothing touched", th.log.calls)
	}
}

func TestBarsRefusesAnInvalidRangeBeforeTouchingAnything(t *testing.T) {
	th := newTestHistory(t)
	cases := map[string][2]string{
		"to before from":       {"2026-11-06", "2026-11-02"},
		"more than 3,660 days": {"2016-01-01", "2026-02-01"},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := th.svc.Bars(t.Context(), th.caller, mustSymbol(t, "AAPL"), mustTradingDate(t, r[0]), mustTradingDate(t, r[1]), th.meta)
			var de *domain.Error
			if !errors.As(err, &de) || de.Code != domain.CodeInvalidInput {
				t.Fatalf("Bars() error = %v, want invalid_input", err)
			}
		})
	}
	if len(th.log.calls) != 0 {
		t.Errorf("calls = %v, want nothing touched", th.log.calls)
	}
}

func TestBarsAcceptsExactlyTheLongestRange(t *testing.T) {
	th := newTestHistory(t)
	from := mustTradingDate(t, "2016-01-04")
	to := from.AddDays(domain.MaxBarRangeDays - 1)
	if _, err := th.svc.Bars(t.Context(), th.caller, mustSymbol(t, "AAPL"), from, to, th.meta); err != nil {
		t.Fatalf("a range of exactly %d days: Bars() error = %v", domain.MaxBarRangeDays, err)
	}
	if _, err := th.svc.Bars(t.Context(), th.caller, mustSymbol(t, "AAPL"), from, to.AddDays(1), th.meta); err == nil {
		t.Fatalf("a range of %d days: Bars() succeeded, want invalid_input", domain.MaxBarRangeDays+1)
	}
}

func TestBarsFirstAskFetchesTheWholeRangeStoresItAndAnswersFromIt(t *testing.T) {
	th := newTestHistory(t)
	th.provide(t, "2026-11-03", "2026-11-04", "2026-11-05")

	got, err := th.bars(t, "AAPL", "2026-11-02", "2026-11-06")
	if err != nil {
		t.Fatalf("Bars() error = %v", err)
	}
	if want := []string{"2026-11-02..2026-11-06"}; !slices.Equal(askedRanges(th), want) {
		t.Errorf("provider asked for %v, want %v", askedRanges(th), want)
	}
	if len(th.repo.saves) != 1 || rangeString(th.repo.saves[0].fetched) != "2026-11-02..2026-11-06" || len(th.repo.saves[0].bars) != 3 {
		t.Fatalf("saves = %+v, want one save of 3 bars for 2026-11-02..2026-11-06", th.repo.saves)
	}
	if !th.repo.saves[0].at.Equal(historyNow) {
		t.Errorf("saved at %v, want the clock's %v", th.repo.saves[0].at, historyNow)
	}
	if want := []string{"2026-11-03", "2026-11-04", "2026-11-05"}; !slices.Equal(datesOf(got.Bars), want) {
		t.Errorf("bars = %v, want %v", datesOf(got.Bars), want)
	}
	if got.Symbol.String() != "AAPL" || got.InstrumentName != "AAPL Inc." || got.Source != domain.BarSourceAlpaca || got.Feed != "sip" || got.IsDemo() {
		t.Errorf("series = %+v", got)
	}
	if got.DataFrom.String() != "2026-11-02" || got.DataThrough.String() != "2026-11-06" {
		t.Errorf("data range = %s..%s, want 2026-11-02..2026-11-06", got.DataFrom, got.DataThrough)
	}
	if len(th.prices.syms) != 1 || len(th.prices.syms[0]) != 1 || th.prices.syms[0][0].String() != "AAPL" {
		t.Errorf("provider asked about %v, want only AAPL", th.prices.syms)
	}
}

func TestBarsAnswersFromTheStoreWithoutTheProviderOnceTheRangeIsCovered(t *testing.T) {
	th := newTestHistory(t)
	th.repo.seedCoverage(domain.BarSourceAlpaca, mustTradingDate(t, "2026-01-05"), mustTradingDate(t, "2026-06-30"),
		serviceBar(t, "2026-02-02"), serviceBar(t, "2026-02-03"))

	got, err := th.bars(t, "AAPL", "2026-02-01", "2026-03-01")
	if err != nil {
		t.Fatalf("Bars() error = %v", err)
	}
	if len(th.prices.asked) != 0 || len(th.repo.saves) != 0 {
		t.Errorf("provider asked %v, saves %d; a covered range must not ask", askedRanges(th), len(th.repo.saves))
	}
	if want := []string{"2026-02-02", "2026-02-03"}; !slices.Equal(datesOf(got.Bars), want) {
		t.Errorf("bars = %v, want %v", datesOf(got.Bars), want)
	}
	if got.DataFrom.String() != "2026-01-05" || got.DataThrough.String() != "2026-06-30" {
		t.Errorf("data range = %s..%s, want the covered 2026-01-05..2026-06-30", got.DataFrom, got.DataThrough)
	}
}

func TestBarsAsksOnlyForWhatTouchesTheCoveredRange(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		want     []string
	}{
		{"before it", "2025-12-01", "2026-02-01", []string{"2025-12-01..2026-01-04"}},
		{"after it", "2026-06-01", "2026-08-01", []string{"2026-07-01..2026-08-01"}},
		{"both sides, front first", "2025-12-01", "2026-08-01", []string{"2025-12-01..2026-01-04", "2026-07-01..2026-08-01"}},
		{"far before, the gap is asked too", "2020-01-01", "2020-02-01", []string{"2020-01-01..2026-01-04"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			th := newTestHistory(t)
			th.repo.seedCoverage(domain.BarSourceAlpaca, mustTradingDate(t, "2026-01-05"), mustTradingDate(t, "2026-06-30"))
			if _, err := th.bars(t, "AAPL", c.from, c.to); err != nil {
				t.Fatalf("Bars() error = %v", err)
			}
			if !slices.Equal(askedRanges(th), c.want) {
				t.Errorf("provider asked for %v, want %v", askedRanges(th), c.want)
			}
			for i, s := range th.repo.saves {
				if rangeString(s.fetched) != c.want[i] {
					t.Errorf("save %d recorded %s, want the range asked %s", i, rangeString(s.fetched), c.want[i])
				}
			}
		})
	}
}

func TestBarsNeverAsksForADayThatHasNotClosed(t *testing.T) {
	th := newTestHistory(t) // today is 11 November, so the last closed day is 10 November
	th.provide(t, "2026-11-09", "2026-11-10")

	got, err := th.bars(t, "AAPL", "2026-11-09", "2026-11-20")
	if err != nil {
		t.Fatalf("Bars() error = %v", err)
	}
	if want := []string{"2026-11-09..2026-11-10"}; !slices.Equal(askedRanges(th), want) {
		t.Errorf("provider asked for %v, want %v (to cut back to yesterday)", askedRanges(th), want)
	}
	if th.repo.saves[0].fetched.To.String() != "2026-11-10" {
		t.Errorf("recorded fetched through %s, want 2026-11-10: a day not yet closed must not be recorded as fetched", th.repo.saves[0].fetched.To)
	}
	if want := []string{"2026-11-09", "2026-11-10"}; !slices.Equal(datesOf(got.Bars), want) {
		t.Errorf("bars = %v, want %v", datesOf(got.Bars), want)
	}
}

func TestBarsOfARangeNothingOfWhichHasClosedAsksNothing(t *testing.T) {
	th := newTestHistory(t)

	got, err := th.bars(t, "AAPL", "2026-11-11", "2026-11-20")
	if err != nil {
		t.Fatalf("Bars() error = %v", err)
	}
	if len(th.prices.asked) != 0 || len(th.repo.saves) != 0 || len(got.Bars) != 0 {
		t.Errorf("asked %v, saves %d, bars %v; want nothing", askedRanges(th), len(th.repo.saves), got.Bars)
	}
	if !got.DataFrom.IsZero() || !got.DataThrough.IsZero() {
		t.Errorf("data range = %s..%s, want none when nothing is stored", got.DataFrom, got.DataThrough)
	}
}

func TestBarsRemembersADayWithoutTradingSoItIsNotAskedAgain(t *testing.T) {
	th := newTestHistory(t) // the provider holds no bar in the range: a holiday week

	for range 2 {
		got, err := th.bars(t, "AAPL", "2026-11-02", "2026-11-04")
		if err != nil || len(got.Bars) != 0 {
			t.Fatalf("Bars() = %v, %v; want no bars and no error", got.Bars, err)
		}
	}
	if len(th.prices.asked) != 1 {
		t.Errorf("provider asked %d times (%v), want once: the empty range is remembered as fetched", len(th.prices.asked), askedRanges(th))
	}
	if len(th.repo.saves) != 1 || len(th.repo.saves[0].bars) != 0 {
		t.Errorf("saves = %+v, want one save with no bars", th.repo.saves)
	}
}

func TestBarsUsesTheConfiguredSourceForEveryReadAndWrite(t *testing.T) {
	th := newTestHistory(t)
	th.provide(t, "2026-11-03")
	if _, err := th.bars(t, "AAPL", "2026-11-02", "2026-11-06"); err != nil {
		t.Fatal(err)
	}
	if len(th.repo.sources) == 0 {
		t.Fatal("the store was never used")
	}
	for _, s := range th.repo.sources {
		if s != domain.BarSourceAlpaca {
			t.Errorf("store used with source %q, want alpaca", s)
		}
	}
}

func TestBarsRefusesABatchFromAnotherSourceThanConfigured(t *testing.T) {
	th := newTestHistory(t)
	th.prices.source = domain.BarSourceDemo // a provider that answers as demo while the service is set to alpaca
	th.provide(t, "2026-11-03")

	_, err := th.bars(t, "AAPL", "2026-11-02", "2026-11-06")
	if !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("Bars() error = %v, want internal", err)
	}
	if len(th.repo.saves) != 0 {
		t.Errorf("saves = %d, want nothing stored under the wrong source", len(th.repo.saves))
	}
}

func TestBarsWhenTheProviderFailsAnswersFromStoredBarsOrFails(t *testing.T) {
	boom := errors.New("alpaca daily bars: unexpected HTTP status 503")

	t.Run("nothing stored: dependency unavailable", func(t *testing.T) {
		th := newTestHistory(t)
		th.prices.serve = func(domain.DateRange) ([]domain.DailyBar, error) { return nil, boom }
		_, err := th.bars(t, "AAPL", "2026-11-02", "2026-11-06")
		if !errors.Is(err, domain.ErrDependencyUnavailable) {
			t.Fatalf("Bars() error = %v, want dependency_unavailable", err)
		}
		if len(th.repo.saves) != 0 {
			t.Errorf("saves = %d, want none after a failed fetch", len(th.repo.saves))
		}
	})
	t.Run("something stored: the stored bars, with how far they go", func(t *testing.T) {
		th := newTestHistory(t)
		th.repo.seedCoverage(domain.BarSourceAlpaca, mustTradingDate(t, "2026-01-05"), mustTradingDate(t, "2026-06-30"), serviceBar(t, "2026-06-29"))
		th.prices.serve = func(domain.DateRange) ([]domain.DailyBar, error) { return nil, boom }

		got, err := th.bars(t, "AAPL", "2026-06-01", "2026-11-10")
		if err != nil {
			t.Fatalf("Bars() error = %v, want the stored bars", err)
		}
		if want := []string{"2026-06-29"}; !slices.Equal(datesOf(got.Bars), want) {
			t.Errorf("bars = %v, want %v", datesOf(got.Bars), want)
		}
		if got.DataThrough.String() != "2026-06-30" {
			t.Errorf("DataThrough = %s, want 2026-06-30 so a client sees how stale it is", got.DataThrough)
		}
		if len(th.repo.saves) != 0 {
			t.Errorf("saves = %d, want none after a failed fetch", len(th.repo.saves))
		}
		logged := th.logs.String()
		if !strings.Contains(logged, "unexpected HTTP status 503") || !strings.Contains(logged, th.meta.RequestID.String()) {
			t.Errorf("log = %q, want the provider error and the request id", logged)
		}
	})
	t.Run("the first side fails, the second is not asked and the stored bars answer", func(t *testing.T) {
		th := newTestHistory(t)
		th.repo.seedCoverage(domain.BarSourceAlpaca, mustTradingDate(t, "2026-01-05"), mustTradingDate(t, "2026-06-30"), serviceBar(t, "2026-02-02"))
		th.prices.serve = func(domain.DateRange) ([]domain.DailyBar, error) { return nil, boom }
		if _, err := th.bars(t, "AAPL", "2025-12-01", "2026-08-01"); err != nil {
			t.Fatalf("Bars() error = %v", err)
		}
		if len(th.prices.asked) != 1 {
			t.Errorf("provider asked %d times (%v), want it to stop after the first failure", len(th.prices.asked), askedRanges(th))
		}
	})
}

func TestBarsStopsWhenTheCallerLeaves(t *testing.T) {
	th := newTestHistory(t)
	th.repo.seedCoverage(domain.BarSourceAlpaca, mustTradingDate(t, "2026-01-05"), mustTradingDate(t, "2026-06-30"), serviceBar(t, "2026-06-29"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := th.svc.Bars(ctx, th.caller, mustSymbol(t, "AAPL"), mustTradingDate(t, "2026-06-01"), mustTradingDate(t, "2026-11-10"), th.meta)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Bars() error = %v, want context.Canceled, not stored bars for a caller who is gone", err)
	}
}

func TestBarsReturnsAStoreFailureAsAnError(t *testing.T) {
	boom := errors.New("connection refused")
	t.Run("save", func(t *testing.T) {
		th := newTestHistory(t)
		th.provide(t, "2026-11-03")
		th.repo.saveErr = boom
		if _, err := th.bars(t, "AAPL", "2026-11-02", "2026-11-06"); !errors.Is(err, boom) {
			t.Fatalf("Bars() error = %v, want the save error", err)
		}
	})
	t.Run("read", func(t *testing.T) {
		th := newTestHistory(t)
		th.repo.readErr = boom
		if _, err := th.bars(t, "AAPL", "2026-11-02", "2026-11-06"); !errors.Is(err, boom) {
			t.Fatalf("Bars() error = %v, want the read error", err)
		}
		if len(th.prices.asked) != 0 {
			t.Error("the provider was asked although the store could not be read")
		}
	})
}

func TestBarsOfAStoredInstrumentNeverSpendsTheLookupQuota(t *testing.T) {
	th := newTestHistory(t)
	if _, err := th.bars(t, "AAPL", "2026-11-02", "2026-11-06"); err != nil {
		t.Fatal(err)
	}
	if th.lookup.calls != 0 || len(*th.scopes) != 0 {
		t.Errorf("lookups = %d, limits asked = %v; a stored instrument needs neither", th.lookup.calls, *th.scopes)
	}
}

func TestBarsOfAnUnseenSymbolIsVerifiedWithinTheLookupLimitThenStored(t *testing.T) {
	th := newTestHistory(t)

	got, err := th.bars(t, "MSFT", "2026-11-02", "2026-11-06")
	if err != nil {
		t.Fatalf("Bars() error = %v", err)
	}
	wantStart := []string{"Instruments.BySymbol", "RateLimits.Admit", "Provider.Lookup", "Instruments.Upsert"}
	if len(th.log.calls) < len(wantStart) || !slices.Equal(th.log.calls[:len(wantStart)], wantStart) {
		t.Errorf("calls = %v, want them to start with %v", th.log.calls, wantStart)
	}
	if !slices.Equal(*th.scopes, []domain.RateLimitScope{domain.RateLimitInstrumentLookupUser}) {
		t.Errorf("limits asked = %v, want instrument_lookup_user", *th.scopes)
	}
	if got.Symbol.String() != "MSFT" || got.InstrumentName != "Microsoft Corporation" {
		t.Errorf("series = %+v, want the verified MSFT", got)
	}
	if len(th.insts.upserted) != 1 || th.insts.upserted[0].Symbol.String() != "MSFT" || th.insts.upserted[0].ID.IsZero() {
		t.Errorf("upserted = %+v, want MSFT with an id", th.insts.upserted)
	}
}

func TestBarsOfAnUnseenSymbolFailsClearly(t *testing.T) {
	t.Run("a symbol that does not exist", func(t *testing.T) {
		th := newTestHistory(t)
		th.lookup.err = domain.ErrUnknownSymbol
		if _, err := th.bars(t, "ZZZZ", "2026-11-02", "2026-11-06"); !errors.Is(err, domain.ErrUnknownSymbol) {
			t.Fatalf("Bars() error = %v, want unknown_symbol", err)
		}
		if len(th.prices.asked) != 0 {
			t.Error("bars were asked for a symbol that does not exist")
		}
	})
	t.Run("the instrument provider is down", func(t *testing.T) {
		th := newTestHistory(t)
		th.lookup.err = errors.New("alpaca assets: timeout")
		if _, err := th.bars(t, "MSFT", "2026-11-02", "2026-11-06"); !errors.Is(err, domain.ErrDependencyUnavailable) {
			t.Fatalf("Bars() error = %v, want dependency_unavailable", err)
		}
	})
	t.Run("past the lookup limit", func(t *testing.T) {
		th := newTestHistory(t)
		th.limits.decision = domain.RateLimitDecision{Allowed: false, RetryAfter: 42 * time.Second, Scope: domain.RateLimitInstrumentLookupUser}
		_, err := th.bars(t, "MSFT", "2026-11-02", "2026-11-06")
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != domain.CodeRateLimited {
			t.Fatalf("Bars() error = %v, want rate_limited", err)
		}
		if th.lookup.calls != 0 || len(th.prices.asked) != 0 {
			t.Errorf("lookups = %d, bars asked %v; a limited caller must reach neither provider", th.lookup.calls, askedRanges(th))
		}
	})
}

func TestNewPriceHistoryChecksItsDependencies(t *testing.T) {
	if _, err := NewPriceHistory(PriceHistoryDeps{}); err == nil {
		t.Error("NewPriceHistory(empty) succeeded, want an error")
	}
	deps := PriceHistoryDeps{
		Store: historyStore{}, UnitOfWork: &fakeUoW{}, Prices: &fakeBarsProvider{}, Instruments: &fakeInstrumentLookup{},
		Source: domain.BarSource("iex"), Clock: newFakeClock(), Limits: newTestPolicy(t), Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
	}
	if _, err := NewPriceHistory(deps); err == nil {
		t.Error("NewPriceHistory(unknown source) succeeded, want an error")
	}
}
