package domain_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"go-backend-stock/internal/core/domain"
)

func TestAddMonthsClampsToTheEndOfTheMonth(t *testing.T) {
	cases := []struct {
		from domain.TradingDate
		n    int
		want string
	}{
		{domain.NewTradingDate(2026, 3, 31), -1, "2026-02-28"},
		{domain.NewTradingDate(2024, 3, 31), -1, "2024-02-29"},
		{domain.NewTradingDate(2026, 11, 10), -12, "2025-11-10"},
		{domain.NewTradingDate(2026, 11, 10), -60, "2021-11-10"},
		{domain.NewTradingDate(2026, 1, 31), -3, "2025-10-31"},
		{domain.NewTradingDate(2026, 5, 31), -3, "2026-02-28"},
	}
	for _, c := range cases {
		if got := c.from.AddMonths(c.n).String(); got != c.want {
			t.Errorf("%s.AddMonths(%d) = %s, want %s", c.from, c.n, got, c.want)
		}
	}
}

func TestStartOfYear(t *testing.T) {
	if got := domain.NewTradingDate(2026, 11, 10).StartOfYear().String(); got != "2026-01-01" {
		t.Fatalf("StartOfYear() = %s", got)
	}
}

func TestPerformanceRangeStart(t *testing.T) {
	through, first := domain.NewTradingDate(2026, 11, 10), domain.NewTradingDate(2020, 5, 4)
	cases := map[string]string{
		"1m": "2026-10-10", "3m": "2026-08-10", "6m": "2026-05-10",
		"ytd": "2026-01-01", "1y": "2025-11-10", "5y": "2021-11-10", "all": "2020-05-04",
	}
	for in, want := range cases {
		r, err := domain.ParsePerformanceRange(in)
		if err != nil {
			t.Fatalf("ParsePerformanceRange(%q) error = %v", in, err)
		}
		if got := r.Start(through, first).String(); got != want {
			t.Errorf("%s: Start = %s, want %s", in, got, want)
		}
	}
	all, _ := domain.ParsePerformanceRange("all")
	oldest := through.AddDays(-(domain.MaxBarRangeDays - 1)).String()
	if got := all.Start(through, domain.NewTradingDate(2010, 1, 4)).String(); got != oldest {
		t.Errorf("all with a first trade older than the cap: Start = %s, want %s", got, oldest)
	}
	if got := all.Start(through, domain.TradingDate{}).String(); got != "2026-11-10" {
		t.Errorf("all without a first trade: Start = %s, want the through date", got)
	}
}

func TestParsePerformanceRangeRefusesAnythingElse(t *testing.T) {
	for _, s := range []string{"", "1M", "2y", " 1y", "1y ", "YTD"} {
		_, err := domain.ParsePerformanceRange(s)
		requireInvalidInput(t, err, "range")
	}
}

func TestParseBenchmarks(t *testing.T) {
	got, err := domain.ParseBenchmarks("")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: got %v, %v", got, err)
	}
	got, err = domain.ParseBenchmarks("SPY,QQQ")
	if err != nil || len(got) != 2 {
		t.Fatalf("SPY,QQQ: got %v, %v", got, err)
	}
	if got[0].Symbol.String() != "SPY" || got[0].Label != "S&P 500 (SPY)" ||
		got[1].Symbol.String() != "QQQ" || got[1].Label != "Nasdaq-100 (QQQ)" {
		t.Errorf("SPY,QQQ: got %+v", got)
	}
	got, err = domain.ParseBenchmarks("QQQ,SPY")
	if err != nil || got[0].Symbol.String() != "QQQ" || got[1].Symbol.String() != "SPY" {
		t.Errorf("order asked must be kept: got %+v, %v", got, err)
	}
	got, err = domain.ParseBenchmarks("QQQ")
	if err != nil || len(got) != 1 || got[0].Symbol.String() != "QQQ" {
		t.Errorf("QQQ: got %+v, %v", got, err)
	}
	for _, s := range []string{"spy", "SPY,SPY", "SPY, QQQ", "AAPL", "SPY,", ",SPY", " "} {
		_, err := domain.ParseBenchmarks(s)
		requireInvalidInput(t, err, "benchmarks")
	}
}

// ---- ComputePerformance (Task 2). Expected values are worked out by hand from ADR-0025. ----

// perfDay returns trading day n of the sample week: 1 is Monday 2026-11-02, 5 is Friday 2026-11-06.
func perfDay(n int) domain.TradingDate { return domain.NewTradingDate(2026, 11, 1+n) }

func perfBar(t *testing.T, date domain.TradingDate, close string) domain.DailyBar {
	t.Helper()
	p, err := domain.ParsePrice(close)
	if err != nil {
		t.Fatalf("ParsePrice(%q) error = %v", close, err)
	}
	return domain.DailyBar{Date: date, Open: p, High: p, Low: p, Close: p}
}

// perfBars builds bars on the given days from closes written "day:close".
func perfBars(t *testing.T, closes ...string) []domain.DailyBar {
	t.Helper()
	out := make([]domain.DailyBar, 0, len(closes))
	for _, c := range closes {
		var n int
		var px string
		if i := strings.IndexByte(c, ':'); i > 0 {
			n, _ = strconv.Atoi(c[:i])
			px = c[i+1:]
		}
		out = append(out, perfBar(t, perfDay(n), px))
	}
	return out
}

func perfTrade(t *testing.T, instrument, portfolio domain.ID, side domain.Side, qty, price, fee string, at time.Time, seq int64) domain.Transaction {
	t.Helper()
	q, err := domain.ParseQuantity(qty)
	if err != nil {
		t.Fatalf("ParseQuantity(%q) error = %v", qty, err)
	}
	p, err := domain.ParsePrice(price)
	if err != nil {
		t.Fatalf("ParsePrice(%q) error = %v", price, err)
	}
	f, err := domain.ParseFee(fee)
	if err != nil {
		t.Fatalf("ParseFee(%q) error = %v", fee, err)
	}
	return domain.Transaction{InstrumentID: instrument, PortfolioID: portfolio, Side: side, Quantity: q, Price: p, Fee: f, ExecutedAt: at, Sequence: seq}
}

// at returns 15:00Z of trading day n (10:00 in New York).
func perfAt(n int) time.Time { return time.Date(2026, 11, 1+n, 15, 0, 0, 0, time.UTC) }

func requireDecimals(t *testing.T, what string, got []*decimal.Decimal, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d values, want %d (%v)", what, len(got), len(want), want)
	}
	for i, w := range want {
		switch {
		case w == "nil" && got[i] != nil:
			t.Errorf("%s[%d] = %s, want nil", what, i, got[i])
		case w != "nil" && got[i] == nil:
			t.Errorf("%s[%d] = nil, want %s", what, i, w)
		case w != "nil" && !got[i].Equal(decimal.RequireFromString(w)):
			t.Errorf("%s[%d] = %s, want %s", what, i, got[i], w)
		}
	}
}

func perfValues(p domain.Performance) []*decimal.Decimal {
	out := make([]*decimal.Decimal, len(p.Points))
	for i, pt := range p.Points {
		out[i] = pt.MarketValue
	}
	return out
}

func perfReturns(p domain.Performance) []*decimal.Decimal {
	out := make([]*decimal.Decimal, len(p.Points))
	for i, pt := range p.Points {
		out[i] = pt.ReturnPercent
	}
	return out
}

func perfDates(p domain.Performance) []string {
	out := make([]string, len(p.Points))
	for i, pt := range p.Points {
		out[i] = pt.Date.String()
	}
	return out
}

func spy(t *testing.T, closes ...string) domain.BenchmarkBars {
	t.Helper()
	b, err := domain.ParseBenchmarks("SPY")
	if err != nil {
		t.Fatal(err)
	}
	return domain.BenchmarkBars{Benchmark: b[0], Bars: perfBars(t, closes...)}
}

func benchReturns(b domain.BenchmarkPerformance) []*decimal.Decimal {
	out := make([]*decimal.Decimal, len(b.Points))
	for i, pt := range b.Points {
		out[i] = pt.ReturnPercent
	}
	return out
}

func TestPerformanceIgnoresBuysAndSellsAsReturn(t *testing.T) {
	aapl, pf := mustID(t), mustID(t)
	out := domain.ComputePerformance(domain.PerformanceInput{
		Trades: []domain.Transaction{
			perfTrade(t, aapl, pf, domain.SideBuy, "10", "98", "1", perfAt(1), 1),
			perfTrade(t, aapl, pf, domain.SideBuy, "10", "110", "0", perfAt(3), 2),
			perfTrade(t, aapl, pf, domain.SideSell, "5", "121", "0", perfAt(4), 3),
		},
		Bars:       map[domain.ID][]domain.DailyBar{aapl: perfBars(t, "1:100", "2:110", "3:110", "4:121", "5:121")},
		Start:      perfDay(1),
		Through:    perfDay(5),
		Benchmarks: []domain.BenchmarkBars{spy(t, "1:500", "2:505", "3:510", "4:520", "5:525")},
	})
	requireDecimals(t, "market_value", perfValues(out), []string{"1000", "1100", "2200", "1815", "1815"})
	requireDecimals(t, "return_percent", perfReturns(out), []string{"0", "10", "10", "21", "21"})
	if out.From != perfDay(1) || out.To != perfDay(5) || out.Coverage != domain.CoverageComplete {
		t.Errorf("From %s To %s Coverage %s", out.From, out.To, out.Coverage)
	}
	requireDecimals(t, "ReturnPercent", []*decimal.Decimal{out.ReturnPercent}, []string{"21"})
	b := out.Benchmarks[0]
	if b.Symbol.String() != "SPY" || b.Label != "S&P 500 (SPY)" {
		t.Errorf("benchmark = %s %q", b.Symbol, b.Label)
	}
	requireDecimals(t, "SPY points", benchReturns(b), []string{"0", "1", "2", "4", "5"})
	requireDecimals(t, "SPY return", []*decimal.Decimal{b.ReturnPercent, b.ExcessReturnPercent}, []string{"5", "16"})
}

func TestPerformanceCountsFeesAndTheTradePrice(t *testing.T) {
	aapl, pf := mustID(t), mustID(t)
	out := domain.ComputePerformance(domain.PerformanceInput{
		Trades: []domain.Transaction{
			perfTrade(t, aapl, pf, domain.SideBuy, "10", "100", "0", perfAt(1), 1),
			perfTrade(t, aapl, pf, domain.SideSell, "5", "100", "50", perfAt(2), 2),
			perfTrade(t, aapl, pf, domain.SideBuy, "10", "60", "0", perfAt(3), 3),
		},
		Bars:    map[domain.ID][]domain.DailyBar{aapl: perfBars(t, "1:100", "2:100", "3:100")},
		Start:   perfDay(1),
		Through: perfDay(3),
	})
	requireDecimals(t, "market_value", perfValues(out), []string{"1000", "500", "1500"})
	// D2: (500 + 450) / 1000 = 0.95. D3: V 1500 over 500 + 600 bought at the start of the day.
	d3 := decimal.RequireFromString("0.95").Mul(decimal.NewFromInt(1500).DivRound(decimal.NewFromInt(1100), 28)).
		RoundBank(28).Sub(decimal.NewFromInt(1)).Mul(decimal.NewFromInt(100))
	requireDecimals(t, "return_percent", perfReturns(out), []string{"0", "-5", d3.String()})
}

func TestPerformanceStaysFlatWhileNothingIsHeld(t *testing.T) {
	aapl, pf := mustID(t), mustID(t)
	out := domain.ComputePerformance(domain.PerformanceInput{
		Trades: []domain.Transaction{
			perfTrade(t, aapl, pf, domain.SideBuy, "10", "100", "0", perfAt(1), 1),
			perfTrade(t, aapl, pf, domain.SideSell, "10", "110", "0", perfAt(2), 2),
			perfTrade(t, aapl, pf, domain.SideBuy, "10", "50", "0", perfAt(4), 3),
		},
		Bars:    map[domain.ID][]domain.DailyBar{aapl: perfBars(t, "1:100", "2:110", "3:110", "4:55")},
		Start:   perfDay(1),
		Through: perfDay(4),
	})
	requireDecimals(t, "market_value", perfValues(out), []string{"1000", "0", "0", "550"})
	requireDecimals(t, "return_percent", perfReturns(out), []string{"0", "10", "10", "21"})
}

func TestPerformanceStartsOnTheFirstDayWithValue(t *testing.T) {
	aapl, pf := mustID(t), mustID(t)
	out := domain.ComputePerformance(domain.PerformanceInput{
		Trades:     []domain.Transaction{perfTrade(t, aapl, pf, domain.SideBuy, "10", "90", "5", perfAt(3), 1)},
		Bars:       map[domain.ID][]domain.DailyBar{aapl: perfBars(t, "1:100", "2:100", "3:100", "4:110", "5:110")},
		Start:      perfDay(1),
		Through:    perfDay(5),
		Benchmarks: []domain.BenchmarkBars{spy(t, "1:400", "2:410", "3:500", "4:505", "5:510")},
	})
	if got := perfDates(out); len(got) != 3 || got[0] != "2026-11-04" {
		t.Fatalf("dates = %v, want 2026-11-04..06", got)
	}
	if out.From != perfDay(3) {
		t.Errorf("From = %s, want %s", out.From, perfDay(3))
	}
	requireDecimals(t, "return_percent", perfReturns(out), []string{"0", "10", "10"})
	requireDecimals(t, "SPY points", benchReturns(out.Benchmarks[0]), []string{"0", "1", "2"})
}

func TestPerformanceLeavesAGapWhenAHeldStockHasNoBar(t *testing.T) {
	aapl, pf := mustID(t), mustID(t)
	out := domain.ComputePerformance(domain.PerformanceInput{
		Trades: []domain.Transaction{
			perfTrade(t, aapl, pf, domain.SideBuy, "10", "100", "0", perfAt(1), 1),
			perfTrade(t, aapl, pf, domain.SideBuy, "10", "110", "0", perfAt(3), 2),
		},
		Bars:       map[domain.ID][]domain.DailyBar{aapl: perfBars(t, "1:100", "2:110", "4:121")},
		Start:      perfDay(1),
		Through:    perfDay(4),
		Benchmarks: []domain.BenchmarkBars{spy(t, "1:500", "2:500", "3:500", "4:500")},
	})
	requireDecimals(t, "market_value", perfValues(out), []string{"1000", "1100", "nil", "2420"})
	requireDecimals(t, "return_percent", perfReturns(out), []string{"0", "10", "nil", "21"})
	if out.Coverage != domain.CoveragePartial {
		t.Errorf("Coverage = %s, want partial", out.Coverage)
	}
	// The benchmark keeps its own value on the gap day: it has a bar there.
	requireDecimals(t, "SPY points", benchReturns(out.Benchmarks[0]), []string{"0", "0", "0", "0"})
}

func TestPerformanceCountsAWeekendTradeOnTheNextTradingDay(t *testing.T) {
	aapl, pf := mustID(t), mustID(t)
	fri, mon := perfDay(5), domain.NewTradingDate(2026, 11, 9)
	out := domain.ComputePerformance(domain.PerformanceInput{
		Trades: []domain.Transaction{
			perfTrade(t, aapl, pf, domain.SideBuy, "10", "100", "0", perfAt(5), 1),
			perfTrade(t, aapl, pf, domain.SideBuy, "10", "100", "0", time.Date(2026, 11, 7, 15, 0, 0, 0, time.UTC), 2),
		},
		Bars:    map[domain.ID][]domain.DailyBar{aapl: {perfBar(t, fri, "100"), perfBar(t, mon, "100")}},
		Start:   fri,
		Through: mon,
	})
	requireDecimals(t, "market_value", perfValues(out), []string{"1000", "2000"})
	requireDecimals(t, "return_percent", perfReturns(out), []string{"0", "0"})
}

func TestPerformanceOfSeveralPortfoliosAddsTheShares(t *testing.T) {
	aapl := mustID(t)
	out := domain.ComputePerformance(domain.PerformanceInput{
		Trades: []domain.Transaction{
			perfTrade(t, aapl, mustID(t), domain.SideBuy, "10", "100", "0", perfAt(1), 1),
			perfTrade(t, aapl, mustID(t), domain.SideBuy, "10", "100", "0", perfAt(1), 2),
		},
		Bars:    map[domain.ID][]domain.DailyBar{aapl: perfBars(t, "1:100")},
		Start:   perfDay(1),
		Through: perfDay(1),
	})
	requireDecimals(t, "market_value", perfValues(out), []string{"2000"})
}

func TestPerformanceWithoutTradesIsEmptyAndComplete(t *testing.T) {
	out := domain.ComputePerformance(domain.PerformanceInput{
		Start:      perfDay(1),
		Through:    perfDay(5),
		Benchmarks: []domain.BenchmarkBars{spy(t, "1:500", "2:505", "3:510", "4:520", "5:525")},
	})
	if out.Points == nil || len(out.Points) != 0 || out.Benchmarks == nil {
		t.Fatalf("Points %v Benchmarks %v, want empty non-nil lists", out.Points, out.Benchmarks)
	}
	if !out.From.IsZero() || !out.To.IsZero() || out.ReturnPercent != nil || out.Coverage != domain.CoverageComplete {
		t.Errorf("From %s To %s Return %v Coverage %s", out.From, out.To, out.ReturnPercent, out.Coverage)
	}
	b := out.Benchmarks[0]
	if b.Points == nil || len(b.Points) != 0 || b.ReturnPercent != nil || b.ExcessReturnPercent != nil {
		t.Errorf("benchmark = %+v, want no points and no return", b)
	}
	if got := domain.ComputePerformance(domain.PerformanceInput{Start: perfDay(1), Through: perfDay(5)}); got.Benchmarks == nil || got.Points == nil {
		t.Error("no benchmark asked must still give empty non-nil lists")
	}
}

func TestPerformanceIsUnavailableWhenNoHeldStockHasABar(t *testing.T) {
	aapl, pf := mustID(t), mustID(t)
	out := domain.ComputePerformance(domain.PerformanceInput{
		Trades:     []domain.Transaction{perfTrade(t, aapl, pf, domain.SideBuy, "10", "100", "0", perfAt(1), 1)},
		Start:      perfDay(1),
		Through:    perfDay(5),
		Benchmarks: []domain.BenchmarkBars{spy(t, "1:500", "2:505", "3:510", "4:520", "5:525")},
	})
	if len(out.Points) != 0 || out.Coverage != domain.CoverageUnavailable {
		t.Errorf("Points %d Coverage %s, want none and unavailable", len(out.Points), out.Coverage)
	}
}

func TestPerformanceIgnoresTradesAfterThrough(t *testing.T) {
	aapl, pf := mustID(t), mustID(t)
	in := domain.PerformanceInput{
		Trades:  []domain.Transaction{perfTrade(t, aapl, pf, domain.SideBuy, "10", "100", "0", perfAt(1), 1)},
		Bars:    map[domain.ID][]domain.DailyBar{aapl: perfBars(t, "1:100", "2:110", "3:121")},
		Start:   perfDay(1),
		Through: perfDay(3),
	}
	want := domain.ComputePerformance(in)
	in.Trades = append(in.Trades, perfTrade(t, aapl, pf, domain.SideBuy, "90", "500", "0", perfAt(4), 2))
	got := domain.ComputePerformance(in)
	requireDecimals(t, "market_value", perfValues(got), []string{"1000", "1100", "1210"})
	requireDecimals(t, "return_percent", perfReturns(got), []string{"0", "10", "21"})
	if got.To != want.To || len(got.Points) != len(want.Points) {
		t.Errorf("a trade after Through changed the result")
	}
}

func TestInstrumentsHeldSince(t *testing.T) {
	a, b, c, pf := mustID(t), mustID(t), mustID(t), mustID(t)
	trades := []domain.Transaction{
		perfTrade(t, a, pf, domain.SideBuy, "5", "10", "0", perfAt(1), 1),  // sold out before from
		perfTrade(t, a, pf, domain.SideSell, "5", "10", "0", perfAt(1), 2), //
		perfTrade(t, b, pf, domain.SideBuy, "5", "10", "0", perfAt(1), 3),  // held across from
		perfTrade(t, c, pf, domain.SideBuy, "5", "10", "0", perfAt(4), 4),  // first bought after from
	}
	got := domain.InstrumentsHeldSince(trades, perfDay(3))
	if len(got) != 2 || got[0] != b || got[1] != c {
		t.Fatalf("InstrumentsHeldSince = %v, want [b c] in order of first appearance", got)
	}
	if got := domain.InstrumentsHeldSince(nil, perfDay(3)); len(got) != 0 {
		t.Errorf("no trades: got %v", got)
	}
}

func TestPerformanceDoesNotBreakWhenASaleFeeIsAboveItsProceeds(t *testing.T) {
	aapl, pf := mustID(t), mustID(t)
	// D1: 3 shares worth 3. D2: all sold at 0.01 with a fee of 2.03, so the sale brings in 0.03 - 2.03 = -2.
	out := domain.ComputePerformance(domain.PerformanceInput{
		Trades: []domain.Transaction{
			perfTrade(t, aapl, pf, domain.SideBuy, "3", "1", "0", perfAt(1), 1),
			perfTrade(t, aapl, pf, domain.SideSell, "3", "0.01", "2.03", perfAt(2), 2),
		},
		Bars:    map[domain.ID][]domain.DailyBar{aapl: perfBars(t, "1:1", "2:0.01")},
		Start:   perfDay(1),
		Through: perfDay(2),
	})
	// (0 - 2) / 3 = -0.6666...667 rounded half-even at 28 places, so the return is that - 1, times 100. A negative
	// numerator is divided and rounded like any other and nothing panics.
	requireDecimals(t, "return_percent", perfReturns(out), []string{"0", "-166.66666666666666666666666667"})
}
