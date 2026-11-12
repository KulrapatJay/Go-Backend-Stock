package domain_test

import (
	"testing"

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
