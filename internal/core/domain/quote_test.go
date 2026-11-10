package domain_test

import (
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func goodQuote(t *testing.T) domain.PriceQuote {
	t.Helper()
	symbol, err := domain.ParseSymbol("AAPL")
	if err != nil {
		t.Fatal(err)
	}
	price, err := domain.ParsePrice("200")
	if err != nil {
		t.Fatal(err)
	}
	return domain.PriceQuote{
		Symbol: symbol, Price: price, Currency: "USD",
		AsOf:      time.Date(2026, time.November, 4, 14, 30, 0, 0, time.UTC),
		FetchedAt: time.Date(2026, time.November, 4, 14, 46, 0, 0, time.UTC),
		Source:    "demo", Feed: "demo", IsDemo: true, SessionStatus: domain.SessionUnknown,
		PriceType: domain.PriceTypeDelayedBarClose,
	}
}

func TestNewPriceQuoteAcceptsAGoodQuoteInUTC(t *testing.T) {
	q := goodQuote(t)
	bangkok := time.FixedZone("ICT", 7*3600)
	q.AsOf, q.FetchedAt = q.AsOf.In(bangkok), q.FetchedAt.In(bangkok)

	got, err := domain.NewPriceQuote(q)
	if err != nil {
		t.Fatalf("NewPriceQuote() error = %v", err)
	}
	if got.AsOf.Location() != time.UTC || got.FetchedAt.Location() != time.UTC || !got.AsOf.Equal(q.AsOf) {
		t.Errorf("times = %v / %v, want the same instants in UTC", got.AsOf, got.FetchedAt)
	}
}

func TestNewPriceQuoteRefusesWhatAnAdapterMustNotBuild(t *testing.T) {
	cases := map[string]func(*domain.PriceQuote){
		"no symbol":                         func(q *domain.PriceQuote) { q.Symbol = domain.Symbol{} },
		"a zero price":                      func(q *domain.PriceQuote) { q.Price = domain.Price{} },
		"another currency":                  func(q *domain.PriceQuote) { q.Currency = "EUR" },
		"as_of after fetched_at":            func(q *domain.PriceQuote) { q.AsOf = q.FetchedAt.Add(time.Second) },
		"no as_of":                          func(q *domain.PriceQuote) { q.AsOf = time.Time{} },
		"no fetched_at":                     func(q *domain.PriceQuote) { q.FetchedAt = time.Time{} },
		"no source":                         func(q *domain.PriceQuote) { q.Source = "" },
		"no feed":                           func(q *domain.PriceQuote) { q.Feed = "" },
		"a control character in the source": func(q *domain.PriceQuote) { q.Source = "de\nmo" },
		"a source over 40 characters": func(q *domain.PriceQuote) {
			q.Source = "0123456789012345678901234567890123456789x"
		},
		"an unknown session": func(q *domain.PriceQuote) { q.SessionStatus = "closed" },
		"no price type":      func(q *domain.PriceQuote) { q.PriceType = "" },
		"an unknown price type": func(q *domain.PriceQuote) {
			q.PriceType = "latest_trade" // not a kind of price this server can serve yet
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := goodQuote(t)
			mutate(&q)
			_, err := domain.NewPriceQuote(q)
			if !errors.Is(err, domain.ErrInternal) {
				t.Fatalf("NewPriceQuote() error = %v, want internal (a bug in the adapter, not user input)", err)
			}
		})
	}
}

func TestAsOfMayEqualFetchedAt(t *testing.T) {
	q := goodQuote(t)
	q.AsOf = q.FetchedAt
	if _, err := domain.NewPriceQuote(q); err != nil {
		t.Fatalf("NewPriceQuote(as_of == fetched_at) error = %v", err)
	}
}

func TestParseSessionStatus(t *testing.T) {
	for _, ok := range []domain.SessionStatus{domain.SessionOpen, domain.SessionMarketClosed, domain.SessionUnknown} {
		got, err := domain.ParseSessionStatus(string(ok))
		if err != nil || got != ok {
			t.Errorf("ParseSessionStatus(%q) = %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"OPEN", "", "halted", "market-closed"} {
		if _, err := domain.ParseSessionStatus(bad); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("ParseSessionStatus(%q) error = %v, want invalid_input", bad, err)
		}
	}
}

// A delayed bar close is, by definition, at least the reference delay behind: a client shows this
// number instead of hard-coding "15 minutes".
func TestDelayedBarCloseIsFifteenMinutesBehind(t *testing.T) {
	if domain.ReferencePriceDelay != 15*time.Minute {
		t.Errorf("ReferencePriceDelay = %v, want 15 minutes", domain.ReferencePriceDelay)
	}
	if got := domain.PriceTypeDelayedBarClose.MinimumDelay(); got != domain.ReferencePriceDelay {
		t.Errorf("MinimumDelay() = %v, want the reference delay", got)
	}
	if got := domain.PriceType("").MinimumDelay(); got != 0 {
		t.Errorf("MinimumDelay() of no price type = %v, want 0", got)
	}
}
