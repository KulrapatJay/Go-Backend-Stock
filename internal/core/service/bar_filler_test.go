package service

import (
	"errors"
	"fmt"
	"testing"

	"go-backend-stock/internal/core/domain"
)

type testFiller struct {
	f      barFiller
	repo   *fakeBarRepo
	prices *fakeBarsProvider
}

// newTestFiller serves a bar for every day of any range it is asked for, as source alpaca.
func newTestFiller(t *testing.T) testFiller {
	t.Helper()
	log := &callLog{}
	repo := newFakeBarRepo(log)
	prices := &fakeBarsProvider{log: log, source: domain.BarSourceAlpaca}
	prices.serve = func(r domain.DateRange) ([]domain.DailyBar, error) {
		var out []domain.DailyBar
		for d := r.From; !d.After(r.To); d = d.AddDays(1) {
			out = append(out, serviceBar(t, d.String()))
		}
		return out, nil
	}
	return testFiller{
		f:      barFiller{store: historyStore{bars: repo}, prices: prices, source: domain.BarSourceAlpaca, clock: &fakeClock{now: historyNow}},
		repo:   repo,
		prices: prices,
	}
}

func fillInstruments(t *testing.T, n int) []domain.Instrument {
	t.Helper()
	out := make([]domain.Instrument, n)
	for i := range out {
		out[i] = mustInstrument(t, fmt.Sprintf("S%02d", i+1), historyNow)
	}
	return out
}

func symbolsAsked(f testFiller) [][]string {
	out := make([][]string, len(f.prices.syms))
	for i, call := range f.prices.syms {
		for _, s := range call {
			out[i] = append(out[i], s.String())
		}
	}
	return out
}

func (f testFiller) covered(id domain.ID) (domain.BarCoverage, bool) {
	c, ok := f.repo.coverage[f.repo.key(id, domain.BarSourceAlpaca)]
	return c, ok
}

func TestFillAsksOncePerMissingRangeForAllSymbolsThatShareIt(t *testing.T) {
	f := newTestFiller(t)
	ins := fillInstruments(t, 3) // S01 and S02 never asked about, S03 covered up to Wednesday
	f.repo.seedFor(ins[2].ID, domain.BarSourceAlpaca, mustTradingDate(t, "2026-11-02"), mustTradingDate(t, "2026-11-04"))

	providerErr, err := f.f.fill(t.Context(), ins, domain.DateRange{From: mustTradingDate(t, "2026-11-02"), To: mustTradingDate(t, "2026-11-06")})
	if providerErr != nil || err != nil {
		t.Fatalf("fill() = %v, %v", providerErr, err)
	}
	asked := []string{}
	for _, r := range f.prices.asked {
		asked = append(asked, rangeString(r))
	}
	if fmt.Sprint(asked) != "[2026-11-02..2026-11-06 2026-11-05..2026-11-06]" ||
		fmt.Sprint(symbolsAsked(f)) != "[[S01 S02] [S03]]" {
		t.Fatalf("asked %v for %v, want {S01 S02} for the whole range then {S03} for the two missing days", asked, symbolsAsked(f))
	}
	for _, in := range ins {
		if c, ok := f.covered(in.ID); !ok || c.From.String() != "2026-11-02" || c.Through.String() != "2026-11-06" {
			t.Errorf("coverage of %s = %v, %v; want 2026-11-02..2026-11-06", in.Symbol, c, ok)
		}
	}
}

func TestFillAsksInChunksOfTenSymbols(t *testing.T) {
	f := newTestFiller(t)
	ins := fillInstruments(t, 23)
	providerErr, err := f.f.fill(t.Context(), ins, domain.DateRange{From: mustTradingDate(t, "2026-11-02"), To: mustTradingDate(t, "2026-11-06")})
	if providerErr != nil || err != nil {
		t.Fatalf("fill() = %v, %v", providerErr, err)
	}
	sizes := []int{}
	for _, call := range f.prices.syms {
		sizes = append(sizes, len(call))
	}
	if fmt.Sprint(sizes) != "[10 10 3]" {
		t.Fatalf("symbols per request = %v, want [10 10 3]", sizes)
	}
	if len(f.repo.saves) != 23 {
		t.Errorf("saves = %d, want one per instrument", len(f.repo.saves))
	}
}

func TestFillAsksNothingWhenEverythingIsCovered(t *testing.T) {
	f := newTestFiller(t)
	ins := fillInstruments(t, 2)
	for _, in := range ins {
		f.repo.seedFor(in.ID, domain.BarSourceAlpaca, mustTradingDate(t, "2026-01-05"), mustTradingDate(t, "2026-11-10"))
	}
	providerErr, err := f.f.fill(t.Context(), ins, domain.DateRange{From: mustTradingDate(t, "2026-11-02"), To: mustTradingDate(t, "2026-11-06")})
	if providerErr != nil || err != nil || len(f.prices.asked) != 0 {
		t.Fatalf("fill() = %v, %v with %d provider calls, want none", providerErr, err, len(f.prices.asked))
	}
}

func TestFillKeepsWhatWasSavedBeforeTheProviderFailed(t *testing.T) {
	f := newTestFiller(t)
	ins := fillInstruments(t, 12)
	calls := 0
	ok := f.prices.serve
	f.prices.serve = func(r domain.DateRange) ([]domain.DailyBar, error) {
		if calls++; calls == 2 {
			return nil, errors.New("provider down")
		}
		return ok(r)
	}
	providerErr, err := f.f.fill(t.Context(), ins, domain.DateRange{From: mustTradingDate(t, "2026-11-02"), To: mustTradingDate(t, "2026-11-06")})
	if providerErr == nil || err != nil {
		t.Fatalf("fill() = %v, %v; want a provider error and no error of ours", providerErr, err)
	}
	for i, in := range ins {
		if _, has := f.covered(in.ID); has != (i < 10) {
			t.Errorf("coverage of %s present = %v, want %v", in.Symbol, has, i < 10)
		}
	}
	if calls != 2 {
		t.Errorf("provider calls = %d, want it to stop at the first failure", calls)
	}
}

func TestFillRefusesAnAnswerFromAnotherSource(t *testing.T) {
	f := newTestFiller(t)
	f.prices.source = domain.BarSourceDemo
	providerErr, err := f.f.fill(t.Context(), fillInstruments(t, 1), domain.DateRange{From: mustTradingDate(t, "2026-11-02"), To: mustTradingDate(t, "2026-11-06")})
	if providerErr != nil || !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("fill() = %v, %v; want an internal error", providerErr, err)
	}
	if len(f.repo.saves) != 0 {
		t.Errorf("saves = %d, want nothing stored under the wrong source", len(f.repo.saves))
	}
}

func TestFillRefusesABarOutsideTheRangeWithoutSavingAnyInstrument(t *testing.T) {
	f := newTestFiller(t)
	f.prices.serve = func(r domain.DateRange) ([]domain.DailyBar, error) {
		return []domain.DailyBar{serviceBar(t, "2026-11-02"), serviceBar(t, "2026-12-01")}, nil
	}
	providerErr, err := f.f.fill(t.Context(), fillInstruments(t, 2), domain.DateRange{From: mustTradingDate(t, "2026-11-02"), To: mustTradingDate(t, "2026-11-06")})
	if providerErr != nil || !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("fill() = %v, %v; want an internal error", providerErr, err)
	}
	if len(f.repo.saves) != 0 {
		t.Errorf("saves = %d, want none after an untrustworthy answer", len(f.repo.saves))
	}
}
