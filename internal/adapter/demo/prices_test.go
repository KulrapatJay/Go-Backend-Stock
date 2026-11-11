package demo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/adapter/demo"
	"go-backend-stock/internal/core/domain"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func symbolsOf(t *testing.T, raw ...string) []domain.Symbol {
	t.Helper()
	out := make([]domain.Symbol, len(raw))
	for i, r := range raw {
		s, err := domain.ParseSymbol(r)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = s
	}
	return out
}

func TestPricesQuoteTheDemoTable(t *testing.T) {
	clock := fixedClock{now: time.Date(2026, 11, 4, 14, 55, 0, 0, time.UTC)}
	cutoff := time.Date(2026, 11, 4, 14, 37, 42, 0, time.UTC)

	batch, err := demo.NewPrices(clock).Quotes(t.Context(), symbolsOf(t, "AAPL", "MSFT", "ZZZZ"), cutoff)
	if err != nil {
		t.Fatalf("Quotes() error = %v", err)
	}

	wantPrices := map[string]string{"AAPL": "200", "MSFT": "420"}
	for symbol, price := range wantPrices {
		q, ok := batch.Quotes[symbol]
		if !ok {
			t.Fatalf("no quote for %s in %+v", symbol, batch)
		}
		if q.Price.String() != price || q.Currency != "USD" || !q.IsDemo || q.Stale || q.Cached ||
			q.Source != "demo" || q.Feed != "demo" || q.SessionStatus != domain.SessionUnknown ||
			q.PriceType != domain.PriceTypeDelayedBarClose {
			t.Errorf("%s quote = %+v", symbol, q)
		}
		if !q.AsOf.Equal(time.Date(2026, 11, 4, 14, 37, 0, 0, time.UTC)) || !q.FetchedAt.Equal(clock.now) {
			t.Errorf("%s as_of = %v, fetched_at = %v; want the bar minute before the cutoff and the clock", symbol, q.AsOf, q.FetchedAt)
		}
		if _, err := domain.NewPriceQuote(q); err != nil {
			t.Errorf("%s: domain.NewPriceQuote() error = %v", symbol, err)
		}
	}
	if len(batch.Unavailable) != 1 || batch.Unavailable[0].String() != "ZZZZ" || len(batch.Quotes) != 2 {
		t.Errorf("batch = %+v, want ZZZZ unavailable and two quotes", batch)
	}
}

func TestPricesCoverTheWholeDemoList(t *testing.T) {
	want := map[string]string{"AAPL": "200", "MSFT": "420", "NVDA": "130", "GOOGL": "175", "AMZN": "190", "META": "520", "TSLA": "250", "SPY": "540", "QQQ": "480"}
	clock := fixedClock{now: time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC)}
	for symbol, price := range want {
		batch, err := demo.NewPrices(clock).Quotes(t.Context(), symbolsOf(t, symbol), clock.now.Add(-15*time.Minute))
		if err != nil || batch.Quotes[symbol].Price.String() != price {
			t.Errorf("%s = %+v, %v; want price %s", symbol, batch.Quotes[symbol], err, price)
		}
	}
}

func TestPricesNeverGoBackInTime(t *testing.T) {
	clock := fixedClock{now: time.Date(2026, 11, 4, 14, 0, 0, 0, time.UTC)} // older than the cutoff
	cutoff := time.Date(2026, 11, 4, 14, 37, 42, 0, time.UTC)

	batch, err := demo.NewPrices(clock).Quotes(t.Context(), symbolsOf(t, "AAPL"), cutoff)
	if err != nil {
		t.Fatalf("Quotes() error = %v", err)
	}
	q := batch.Quotes["AAPL"]
	if q.FetchedAt.Before(q.AsOf) {
		t.Errorf("fetched_at %v is before as_of %v", q.FetchedAt, q.AsOf)
	}
	if _, err := domain.NewPriceQuote(q); err != nil {
		t.Errorf("domain.NewPriceQuote() error = %v", err)
	}
}

func TestPricesStopOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := demo.NewPrices(fixedClock{now: time.Now()}).Quotes(ctx, symbolsOf(t, "AAPL"), time.Now())
	if err == nil || errors.Is(err, domain.ErrUnknownSymbol) {
		t.Fatalf("Quotes() error = %v, want the context's error", err)
	}
}
