package demo_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"go-backend-stock/internal/adapter/demo"
	"go-backend-stock/internal/core/domain"
)

var demoNow = fixedClock{now: time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)} // Wednesday morning in New York

func date(t *testing.T, s string) domain.TradingDate {
	t.Helper()
	d, err := domain.ParseTradingDate("date", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func demoBars(t *testing.T, from, to string, symbols ...string) domain.DailyBarBatch {
	t.Helper()
	batch, err := demo.NewPrices(demoNow).DailyBars(t.Context(), symbolsOf(t, symbols...), date(t, from), date(t, to))
	if err != nil {
		t.Fatalf("DailyBars() error = %v", err)
	}
	return batch
}

// sameBar compares by value: a struct holding decimals cannot be compared with ==, which compares pointers.
func sameBar(a, b domain.DailyBar) bool {
	return a.Date.Equal(b.Date) && a.Open.String() == b.Open.String() && a.High.String() == b.High.String() &&
		a.Low.String() == b.Low.String() && a.Close.String() == b.Close.String() && a.Volume.String() == b.Volume.String()
}

func TestDailyBarsAreMarkedAsDemoData(t *testing.T) {
	batch := demoBars(t, "2026-11-02", "2026-11-10", "AAPL")
	if batch.Source != domain.BarSourceDemo || batch.Feed != "demo" || !batch.IsDemo() {
		t.Errorf("batch source/feed = %s/%s, demo %v; want demo/demo, demo", batch.Source, batch.Feed, batch.IsDemo())
	}
}

func TestDailyBarsGiveTheSameBarsEveryTime(t *testing.T) {
	a := demoBars(t, "2026-01-05", "2026-06-30", "AAPL", "SPY")
	b := demoBars(t, "2026-01-05", "2026-06-30", "SPY", "AAPL")
	for _, symbol := range []string{"AAPL", "SPY"} {
		if len(a.Bars[symbol]) == 0 || len(a.Bars[symbol]) != len(b.Bars[symbol]) {
			t.Fatalf("%s: %d and %d bars, want the same non-empty count", symbol, len(a.Bars[symbol]), len(b.Bars[symbol]))
		}
		for i := range a.Bars[symbol] {
			if !sameBar(a.Bars[symbol][i], b.Bars[symbol][i]) {
				t.Fatalf("%s bar %d differs between two calls: %+v vs %+v", symbol, i, a.Bars[symbol][i], b.Bars[symbol][i])
			}
		}
	}
}

func TestADemoBarDoesNotDependOnTheRangeItWasAskedIn(t *testing.T) {
	// The bars of 2026-03-02 asked inside a long range and inside a short one must be the same bar, or two
	// fetches that are stored side by side would contradict each other.
	long := demoBars(t, "2026-01-05", "2026-06-30", "MSFT").Bars["MSFT"]
	short := demoBars(t, "2026-03-02", "2026-03-02", "MSFT").Bars["MSFT"]
	if len(short) != 1 {
		t.Fatalf("short range has %d bars, want 1", len(short))
	}
	for _, b := range long {
		if b.Date.Equal(short[0].Date) && !sameBar(b, short[0]) {
			t.Errorf("bar of %s = %+v in the long range and %+v in the short one", b.Date, b, short[0])
		}
	}
}

func TestDailyBarsDifferBetweenSymbolsAndDays(t *testing.T) {
	bars := demoBars(t, "2026-11-02", "2026-11-06", "AAPL", "MSFT")
	if bars.Bars["AAPL"][0].Close.String() == bars.Bars["MSFT"][0].Close.String() {
		t.Error("two symbols have the same close on the same day")
	}
	first, last := bars.Bars["AAPL"][0], bars.Bars["AAPL"][len(bars.Bars["AAPL"])-1]
	if first.Close.String() == last.Close.String() && first.Open.String() == last.Open.String() {
		t.Error("two days of one symbol have the same prices")
	}
}

func TestDailyBarsAreOnlyWeekdaysOldestFirstAndValid(t *testing.T) {
	list := demoBars(t, "2026-10-01", "2026-11-10", "TSLA").Bars["TSLA"]
	if len(list) == 0 {
		t.Fatal("no bars")
	}
	for i, b := range list {
		if wd := b.Date.UTC().Weekday(); wd == time.Saturday || wd == time.Sunday {
			t.Errorf("bar %s falls on a %s", b.Date, wd)
		}
		if i > 0 && !list[i-1].Date.Before(b.Date) {
			t.Errorf("bars are not oldest first at %s", b.Date)
		}
		if _, err := domain.NewDailyBar(b); err != nil {
			t.Errorf("bar %s is not valid: %v", b.Date, err)
		}
		hi, lo := b.High.Decimal(), b.Low.Decimal()
		if hi.LessThan(b.Open.Decimal()) || hi.LessThan(b.Close.Decimal()) || lo.GreaterThan(b.Open.Decimal()) || lo.GreaterThan(b.Close.Decimal()) {
			t.Errorf("bar %s has open or close outside low..high: %+v", b.Date, b)
		}
	}
	if got := len(list); got != 29 { // 1 Oct 2026 is a Thursday; 1 Oct..10 Nov has 29 weekdays
		t.Errorf("bars = %d, want 29 weekdays", got)
	}
}

func TestDailyBarsPricesStayNearTheDemoPrice(t *testing.T) {
	for _, b := range demoBars(t, "2016-01-04", "2026-11-10", "AAPL").Bars["AAPL"] {
		close := b.Close.Decimal()
		if close.LessThan(decimal.NewFromInt(100)) || close.GreaterThan(decimal.NewFromInt(300)) { // the demo price is 200
			t.Fatalf("bar %s closes at %s, far from the demo price 200", b.Date, close)
		}
	}
}

func TestDailyBarsNeverIncludeADayThatHasNotClosed(t *testing.T) {
	list := demoBars(t, "2026-11-05", "2026-11-20", "AAPL").Bars["AAPL"]
	if len(list) == 0 || list[len(list)-1].Date.String() != "2026-11-10" {
		t.Fatalf("last bar = %v, want 2026-11-10: the clock says 11 November, so that day has not closed", list)
	}
	none := demoBars(t, "2026-11-11", "2026-11-20", "AAPL").Bars["AAPL"]
	if len(none) != 0 {
		t.Errorf("bars = %v, want none for days that have not closed", none)
	}
}

func TestDailyBarsStartWhereTheFreeAlpacaHistoryStarts(t *testing.T) {
	list := demoBars(t, "2010-01-01", "2016-01-08", "AAPL").Bars["AAPL"]
	if len(list) != 5 || list[0].Date.String() != "2016-01-04" {
		t.Errorf("bars = %d starting %v, want 5 starting 2016-01-04", len(list), list)
	}
}

func TestDailyBarsOfAnUnknownSymbolAreAnEmptyList(t *testing.T) {
	batch := demoBars(t, "2026-11-02", "2026-11-10", "AAPL", "ZZZZ")
	if len(batch.Bars["ZZZZ"]) != 0 || len(batch.Bars["AAPL"]) == 0 {
		t.Errorf("bars = %v, want ZZZZ empty and AAPL not", batch.Bars)
	}
}

func TestDailyBarsCoverQQQAndEveryDemoSymbol(t *testing.T) {
	for _, s := range []string{"AAPL", "MSFT", "NVDA", "GOOGL", "AMZN", "META", "TSLA", "SPY", "QQQ"} {
		if len(demoBars(t, "2026-11-02", "2026-11-10", s).Bars[s]) == 0 {
			t.Errorf("%s has no demo bars", s)
		}
	}
}

func TestDailyBarsRefuseAnImpossibleRangeAndACancelledContext(t *testing.T) {
	p := demo.NewPrices(demoNow)
	if _, err := p.DailyBars(t.Context(), symbolsOf(t, "AAPL"), date(t, "2026-11-10"), date(t, "2026-11-02")); err == nil {
		t.Error("from after to: DailyBars() succeeded, want an error")
	}
	if _, err := p.DailyBars(t.Context(), symbolsOf(t, "AAPL"), domain.TradingDate{}, date(t, "2026-11-02")); err == nil {
		t.Error("no from: DailyBars() succeeded, want an error")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.DailyBars(ctx, symbolsOf(t, "AAPL"), date(t, "2026-11-02"), date(t, "2026-11-10")); err == nil {
		t.Error("cancelled context: DailyBars() succeeded, want an error")
	}
}
