package alpaca

import (
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func cachedQuote(t *testing.T, symbol, price string, fetchedAt time.Time) domain.PriceQuote {
	t.Helper()
	p, err := domain.ParsePrice(price)
	if err != nil {
		t.Fatal(err)
	}
	q, err := domain.NewPriceQuote(domain.PriceQuote{
		Symbol: mustSymbol(t, symbol), Price: p, Currency: "USD", AsOf: fetchedAt.Add(-time.Minute), FetchedAt: fetchedAt,
		Source: "alpaca", Feed: "sip", SessionStatus: domain.SessionOpen, PriceType: domain.PriceTypeDelayedBarClose,
	})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestCacheAgeRules(t *testing.T) {
	c := newQuoteCache(10)
	t0 := at(2026, 11, 3, 15, 0, 0)
	c.put(cachedQuote(t, "AAPL", "100", t0))

	for _, tc := range []struct {
		name   string
		age    time.Duration
		maxAge time.Duration
		want   bool
	}{
		{"fresh just under a minute", 59 * time.Second, freshTTL, true},
		{"fresh at exactly a minute", 60 * time.Second, freshTTL, false},
		{"outage just under seven days", 7*24*time.Hour - time.Second, outageMaxAge, true},
		{"outage at exactly seven days", 7 * 24 * time.Hour, outageMaxAge, false},
	} {
		if _, ok := c.get("AAPL", t0.Add(tc.age), tc.maxAge); ok != tc.want {
			t.Errorf("%s: get = %v, want %v", tc.name, ok, tc.want)
		}
	}
	if _, ok := c.get("MSFT", t0, freshTTL); ok {
		t.Error("a symbol that was never cached was found")
	}
}

func TestCacheKeepsAtMostFiveHundredSymbols(t *testing.T) {
	c := newQuoteCache(cacheMax)
	t0 := at(2026, 11, 3, 15, 0, 0)
	for i := range cacheMax + 1 {
		c.put(cachedQuote(t, "S"+strconv.Itoa(i), "1", t0.Add(time.Duration(i)*time.Second)))
	}
	now := t0.Add(time.Duration(cacheMax) * time.Second)
	if _, ok := c.get("S0", now, outageMaxAge); ok {
		t.Error("the oldest symbol was kept beyond the limit")
	}
	if _, ok := c.get("S"+strconv.Itoa(cacheMax), now, outageMaxAge); !ok {
		t.Error("the newest symbol was dropped")
	}
	if n := c.size(); n != cacheMax {
		t.Errorf("cache holds %d symbols, want %d", n, cacheMax)
	}
	c.put(cachedQuote(t, "S5", "2", now)) // replacing a known symbol must not evict another
	if n := c.size(); n != cacheMax {
		t.Errorf("cache holds %d symbols after a replacement, want %d", n, cacheMax)
	}
}

func TestFreshQuotesAreServedFromCacheForSixtySeconds(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, aaplBars("189.5"))
	t0 := f.clock.now

	first, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f))
	if err != nil || first.Quotes["AAPL"].Cached {
		t.Fatalf("first Quotes = %+v, %v; want a live quote", first, err)
	}
	f.clock.advance(59 * time.Second)
	second, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f))
	if err != nil {
		t.Fatal(err)
	}
	q := second.Quotes["AAPL"]
	if f.bars.hits.Load() != 1 || !q.Cached || q.Stale || !q.FetchedAt.Equal(t0) || q.Price.String() != "189.5" {
		t.Fatalf("second Quotes = %+v after %d bars requests; want the cached quote (fetched_at %v) with no new request", q, f.bars.hits.Load(), t0)
	}
	f.clock.advance(time.Second) // 60 seconds
	third, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f))
	if err != nil || f.bars.hits.Load() != 2 || third.Quotes["AAPL"].Cached {
		t.Fatalf("third Quotes = %+v, %v after %d requests; want a fresh request at 60 seconds", third, err, f.bars.hits.Load())
	}
}

func TestOnlyMissingSymbolsAreAskedFor(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, func(q url.Values) (int, string) {
		if q.Get("symbols") == "AAPL" {
			return 200, `{"bars":{"AAPL":[{"t":"2026-11-03T14:44:00Z","c":1}]},"next_page_token":null}`
		}
		return 200, `{"bars":{"MSFT":[{"t":"2026-11-03T14:44:00Z","c":2}]},"next_page_token":null}`
	})
	if _, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f)); err != nil {
		t.Fatal(err)
	}

	batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL", "MSFT"), cutoffOf(f))

	if err != nil || len(f.bars.queries) != 2 || f.bars.query(1).Get("symbols") != "MSFT" {
		t.Fatalf("queries = %v, error = %v; want the second request to ask for MSFT only", f.bars.queries, err)
	}
	if !batch.Quotes["AAPL"].Cached || batch.Quotes["MSFT"].Cached || len(batch.Quotes) != 2 {
		t.Errorf("batch = %+v, want AAPL from the cache and MSFT live", batch)
	}
}

func TestOutageFallsBackToQuotesUpToSevenDaysOld(t *testing.T) {
	var down atomic.Bool
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, func(url.Values) (int, string) {
		if down.Load() {
			return 503, ``
		}
		return aaplBars("189.5")(nil)
	})
	original, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f))
	if err != nil {
		t.Fatal(err)
	}
	down.Store(true)

	f.clock.advance(7*24*time.Hour - time.Hour) // 6 days 23 hours
	batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL", "MSFT"), cutoffOf(f))
	if err != nil {
		t.Fatalf("Quotes() error = %v, want the stored quote during the outage", err)
	}
	q := batch.Quotes["AAPL"]
	if !q.Cached || !q.Stale || !q.AsOf.Equal(original.Quotes["AAPL"].AsOf) || !q.FetchedAt.Equal(original.Quotes["AAPL"].FetchedAt) || q.Price.String() != "189.5" {
		t.Errorf("fallback quote = %+v, want the original with cached and stale set and its own as_of", q)
	}
	if _, priced := batch.Quotes["MSFT"]; priced {
		t.Error("MSFT was priced although nothing was ever stored for it")
	}
	for _, s := range batch.Unavailable {
		if s.String() == "MSFT" {
			t.Error("MSFT was reported as having no price, but the provider never answered about it: it belongs in neither list")
		}
	}

	f.clock.advance(time.Hour) // exactly 7 days
	if batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f)); err == nil || len(batch.Quotes) != 0 {
		t.Errorf("Quotes at 7 days = %+v, %v; want an error and no price", batch, err)
	}
	other, err := newPricesFixtureForOutage(t)
	if err == nil {
		t.Errorf("a provider that never answered and has no stored quote returned %+v, want an error", other)
	}
}

// newPricesFixtureForOutage is a provider whose bars never answer and that has nothing stored.
func newPricesFixtureForOutage(t *testing.T) (domain.QuoteBatch, error) {
	t.Helper()
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, func(url.Values) (int, string) { return 500, `` })
	return f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f))
}

func TestCacheIsSafeForConcurrentQuotes(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, aaplBars("189.5"))
	var wg sync.WaitGroup
	failures := make(chan string, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f))
			if err != nil || batch.Quotes["AAPL"].Price.String() != "189.5" {
				failures <- "wrong answer"
			}
		}()
	}
	wg.Wait()
	close(failures)
	for msg := range failures {
		t.Error(msg)
	}
}
