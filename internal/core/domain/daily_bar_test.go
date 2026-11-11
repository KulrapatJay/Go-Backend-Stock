package domain_test

import (
	"testing"

	"go-backend-stock/internal/core/domain"
)

func mustPrice(t *testing.T, s string) domain.Price {
	t.Helper()
	p, err := domain.ParsePrice(s)
	if err != nil {
		t.Fatalf("ParsePrice(%q) error = %v", s, err)
	}
	return p
}

func mustVolume(t *testing.T, s string) domain.Volume {
	t.Helper()
	v, err := domain.ParseVolume(s)
	if err != nil {
		t.Fatalf("ParseVolume(%q) error = %v", s, err)
	}
	return v
}

func goodBar(t *testing.T) domain.DailyBar {
	t.Helper()
	return domain.DailyBar{
		Date: mustDate(t, "2026-11-10"),
		Open: mustPrice(t, "351.35"), High: mustPrice(t, "353.87"), Low: mustPrice(t, "346.4605"), Close: mustPrice(t, "348.51"),
		Volume: mustVolume(t, "29910098"),
	}
}

func TestParseVolumeAcceptsAWholeNumberOfSharesFromZero(t *testing.T) {
	for _, s := range []string{"0", "1", "29910098", "999999999999999999"} {
		if got := mustVolume(t, s).String(); got != s {
			t.Errorf("String() = %q, want %q", got, s)
		}
	}
	for _, s := range []string{"", "-1", "1.5", "1e6", "1,000", " 1", "0x10", "1000000000000000000", "NaN"} {
		if _, err := domain.ParseVolume(s); err == nil {
			t.Errorf("ParseVolume(%q) succeeded, want invalid_input", s)
		} else {
			requireInvalidInput(t, err, "volume")
		}
	}
}

func TestNewDailyBarAcceptsAGoodBar(t *testing.T) {
	if _, err := domain.NewDailyBar(goodBar(t)); err != nil {
		t.Fatalf("NewDailyBar() error = %v", err)
	}
}

func TestNewDailyBarRefusesWhatAnAdapterMustNotBuild(t *testing.T) {
	cases := map[string]func(*domain.DailyBar){
		"no date":        func(b *domain.DailyBar) { b.Date = domain.TradingDate{} },
		"a zero open":    func(b *domain.DailyBar) { b.Open = domain.Price{} },
		"a zero high":    func(b *domain.DailyBar) { b.High = domain.Price{} },
		"a zero low":     func(b *domain.DailyBar) { b.Low = domain.Price{} },
		"a zero close":   func(b *domain.DailyBar) { b.Close = domain.Price{} },
		"high below low": func(b *domain.DailyBar) { b.High, b.Low = b.Low, b.High },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := goodBar(t)
			mutate(&b)
			_, err := domain.NewDailyBar(b)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}

func TestTheZeroVolumeIsAValidVolumeOfZero(t *testing.T) {
	b := goodBar(t)
	b.Volume = domain.Volume{}
	if _, err := domain.NewDailyBar(b); err != nil {
		t.Fatalf("a bar with volume 0 is valid, got %v", err)
	}
	if got := (domain.Volume{}).String(); got != "0" {
		t.Errorf("zero Volume String() = %q, want 0", got)
	}
}

func TestParseBarSourceAcceptsExactlyAlpacaOrDemo(t *testing.T) {
	for _, s := range []string{"alpaca", "demo"} {
		if got, err := domain.ParseBarSource(s); err != nil || string(got) != s {
			t.Errorf("ParseBarSource(%q) = %q, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "Alpaca", "iex", "alpaca "} {
		if _, err := domain.ParseBarSource(s); err == nil {
			t.Errorf("ParseBarSource(%q) succeeded", s)
		}
	}
}

func TestMissingRangesAsksOnlyForWhatTouchesTheCoveredRange(t *testing.T) {
	d := func(s string) domain.TradingDate { return mustDate(t, s) }
	covered := domain.BarCoverage{From: d("2026-01-05"), Through: d("2026-06-30")}
	type r = domain.DateRange
	cases := []struct {
		name     string
		coverage *domain.BarCoverage
		from, to string
		want     []r
	}{
		{"nothing is covered yet", nil, "2026-02-01", "2026-03-01", []r{{From: d("2026-02-01"), To: d("2026-03-01")}}},
		{"inside the covered range", &covered, "2026-02-01", "2026-03-01", nil},
		{"exactly the covered range", &covered, "2026-01-05", "2026-06-30", nil},
		{"before the covered range, touching it", &covered, "2025-12-01", "2026-02-01", []r{{From: d("2025-12-01"), To: d("2026-01-04")}}},
		{"after the covered range, touching it", &covered, "2026-06-01", "2026-08-01", []r{{From: d("2026-07-01"), To: d("2026-08-01")}}},
		{"on both sides", &covered, "2025-12-01", "2026-08-01", []r{
			{From: d("2025-12-01"), To: d("2026-01-04")}, {From: d("2026-07-01"), To: d("2026-08-01")}}},
		{"wholly before, with a gap to the covered range: the gap is asked too", &covered, "2020-01-01", "2020-02-01",
			[]r{{From: d("2020-01-01"), To: d("2026-01-04")}}},
		{"wholly after, with a gap from the covered range: the gap is asked too", &covered, "2026-09-01", "2026-09-30",
			[]r{{From: d("2026-07-01"), To: d("2026-09-30")}}},
		{"one day", nil, "2026-02-02", "2026-02-02", []r{{From: d("2026-02-02"), To: d("2026-02-02")}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := domain.MissingRanges(c.coverage, d(c.from), d(c.to))
			if len(got) != len(c.want) {
				t.Fatalf("MissingRanges = %v, want %v", got, c.want)
			}
			for i := range got {
				if !got[i].From.Equal(c.want[i].From) || !got[i].To.Equal(c.want[i].To) {
					t.Errorf("range %d = %s..%s, want %s..%s", i, got[i].From, got[i].To, c.want[i].From, c.want[i].To)
				}
			}
		})
	}
}

func TestBarCoverageExtendedByKeepsOneUnbrokenRange(t *testing.T) {
	d := func(s string) domain.TradingDate { return mustDate(t, s) }
	covered := domain.BarCoverage{From: d("2026-01-05"), Through: d("2026-06-30")}
	got := covered.ExtendedBy(d("2025-12-01"), d("2026-02-01"))
	if got.From.String() != "2025-12-01" || got.Through.String() != "2026-06-30" {
		t.Errorf("ExtendedBy = %s..%s, want 2025-12-01..2026-06-30", got.From, got.Through)
	}
	got = covered.ExtendedBy(d("2026-07-01"), d("2026-09-01"))
	if got.From.String() != "2026-01-05" || got.Through.String() != "2026-09-01" {
		t.Errorf("ExtendedBy = %s..%s, want 2026-01-05..2026-09-01", got.From, got.Through)
	}
}

func TestBarContractConstants(t *testing.T) {
	if domain.MaxBarRangeDays != 3660 || domain.BarAdjustmentRaw != "raw" || domain.BarTimeframeDaily != "1d" {
		t.Errorf("constants = %d %q %q", domain.MaxBarRangeDays, domain.BarAdjustmentRaw, domain.BarTimeframeDaily)
	}
}

func TestBarSourceFeedIsWhatAClientIsTold(t *testing.T) {
	if domain.BarSourceAlpaca.Feed() != "sip" || domain.BarSourceDemo.Feed() != "demo" {
		t.Errorf("feeds = %q, %q; want sip and demo", domain.BarSourceAlpaca.Feed(), domain.BarSourceDemo.Feed())
	}
}
