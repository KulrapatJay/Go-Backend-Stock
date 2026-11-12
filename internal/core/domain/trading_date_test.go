package domain_test

import (
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func mustDate(t *testing.T, s string) domain.TradingDate {
	t.Helper()
	d, err := domain.ParseTradingDate("date", s)
	if err != nil {
		t.Fatalf("ParseTradingDate(%q) error = %v", s, err)
	}
	return d
}

func TestParseTradingDateAcceptsOnlyARealCalendarDate(t *testing.T) {
	for _, s := range []string{"2026-11-10", "2016-01-04", "2024-02-29"} {
		if got := mustDate(t, s).String(); got != s {
			t.Errorf("String() = %q, want %q", got, s)
		}
	}
	for _, s := range []string{
		"", "2026-11-10T00:00:00Z", "2026-1-10", "20261110", "2026-13-01", "2026-02-30", "2025-02-29",
		" 2026-11-10", "2026-11-10 ", "+2026-11-10", "2026-11-1a", "٢٠٢٦-١١-١٠", "0000-01-01",
	} {
		if _, err := domain.ParseTradingDate("from", s); err == nil {
			t.Errorf("ParseTradingDate(%q) succeeded, want invalid_input", s)
		} else {
			requireInvalidInput(t, err, "from")
		}
	}
}

func TestTradingDateOrderingAndArithmetic(t *testing.T) {
	a, b := mustDate(t, "2026-02-27"), mustDate(t, "2026-03-02")
	if !a.Before(b) || b.Before(a) || a.Before(a) || !b.After(a) || !a.Equal(mustDate(t, "2026-02-27")) {
		t.Fatal("Before/After/Equal disagree with the calendar")
	}
	if got := a.AddDays(3).String(); got != "2026-03-02" {
		t.Errorf("AddDays(3) = %s, want 2026-03-02 (February 2026 has 28 days)", got)
	}
	if got := b.AddDays(-3).String(); got != "2026-02-27" {
		t.Errorf("AddDays(-3) = %s, want 2026-02-27", got)
	}
	if got := a.DaysUntil(b); got != 3 {
		t.Errorf("DaysUntil() = %d, want 3", got)
	}
	if got := b.DaysUntil(a); got != -3 {
		t.Errorf("DaysUntil() = %d, want -3", got)
	}
	if !(domain.TradingDate{}).IsZero() || a.IsZero() {
		t.Error("only the zero value is zero")
	}
}

func TestTradingDateIsNotMovedByATimezone(t *testing.T) {
	// 2026-11-10 23:30 in Bangkok is still 2026-11-10 for the date itself; the date has no zone to shift.
	if got := mustDate(t, "2026-11-10").UTC().Format(time.RFC3339); got != "2026-11-10T00:00:00Z" {
		t.Errorf("UTC() = %s", got)
	}
}

func TestMarketDateIsTheNewYorkDate(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"just after midnight in New York (EST)", time.Date(2026, 11, 10, 5, 0, 0, 0, time.UTC), "2026-11-10"},
		{"just before midnight in New York (EST)", time.Date(2026, 11, 10, 4, 59, 59, 0, time.UTC), "2026-11-09"},
		{"just after midnight in New York (EDT)", time.Date(2026, 7, 1, 4, 0, 0, 0, time.UTC), "2026-07-01"},
		{"just before midnight in New York (EDT)", time.Date(2026, 7, 1, 3, 59, 59, 0, time.UTC), "2026-06-30"},
		{"the day clocks go forward", time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC), "2026-03-08"},
		{"the day clocks go back", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), "2026-11-01"},
		{"an instant given in another zone", time.Date(2026, 11, 10, 11, 0, 0, 0, time.FixedZone("ICT", 7*3600)), "2026-11-09"},
	}
	for _, c := range cases {
		if got := domain.MarketDate(c.at).String(); got != c.want {
			t.Errorf("%s: MarketDate(%s) = %s, want %s", c.name, c.at.Format(time.RFC3339), got, c.want)
		}
	}
}

func TestMarketDayStartIsMidnightInNewYork(t *testing.T) {
	cases := map[string]string{
		"2026-11-10": "2026-11-10T05:00:00Z", // EST
		"2026-07-01": "2026-07-01T04:00:00Z", // EDT
		"2026-03-08": "2026-03-08T05:00:00Z", // midnight is still EST on the day clocks go forward
	}
	for date, want := range cases {
		if got := mustDate(t, date).MarketDayStart().UTC().Format(time.RFC3339); got != want {
			t.Errorf("MarketDayStart(%s) = %s, want %s", date, got, want)
		}
	}
}

func TestMarketDateOfABarTimestampIsTheBarDay(t *testing.T) {
	// Alpaca stamps a daily bar with midnight New York as UTC: 04:00Z in summer, 05:00Z in winter.
	for at, want := range map[string]string{"2026-11-10T05:00:00Z": "2026-11-10", "2026-07-01T04:00:00Z": "2026-07-01"} {
		ts, err := time.Parse(time.RFC3339, at)
		if err != nil {
			t.Fatal(err)
		}
		if got := domain.MarketDate(ts).String(); got != want {
			t.Errorf("MarketDate(%s) = %s, want %s", at, got, want)
		}
	}
}
