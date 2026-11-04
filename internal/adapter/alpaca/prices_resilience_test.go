package alpaca

import (
	"context"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// A calendar that hangs must not use up the time the bars need: the bars are still read and the price
// says session unknown.
func TestSlowCalendarLeavesTimeForTheBars(t *testing.T) {
	clk := &clock{now: at(2026, 11, 3, 15, 0, 10)}
	calendar := newFakeAlpaca(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	bars := newBarsServer(t, aaplBars("123.45"))
	p, err := NewPrices(PricesConfig{
		DataBaseURL: bars.URL, TradingBaseURL: calendar.URL, KeyID: testKeyID, Secret: testSecret,
		Timeout: time.Second, Now: clk.Now,
	})
	if err != nil {
		t.Fatalf("NewPrices() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2200*time.Millisecond)
	defer cancel()

	batch, err := p.Quotes(ctx, symbols(t, "AAPL"), clk.now.Add(-15*time.Minute))

	q, ok := batch.Quotes["AAPL"]
	if err != nil || !ok || q.SessionStatus != domain.SessionUnknown || !q.Stale {
		t.Fatalf("Quotes() = %+v, %v; want the bars priced as unknown and stale although the calendar hangs", batch, err)
	}
}

// The calendar of a date does not change during the day, so an entry past its time to live is still
// better than no calendar when the refetch fails.
func TestExpiredCalendarIsKeptWhenTheRefetchFails(t *testing.T) {
	calendarUp := atomic.Bool{}
	calendarUp.Store(true)
	f := newPricesFixture(t, func() (int, string) {
		if calendarUp.Load() {
			return 200, oneSessionCalendar
		}
		return 503, `{}`
	}, aaplBars("123.45"))
	if _, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f)); err != nil {
		t.Fatalf("first Quotes() error = %v", err)
	}

	calendarUp.Store(false)
	f.clock.advance(7 * time.Hour)
	batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f))

	if q := batch.Quotes["AAPL"]; err != nil || q.SessionStatus != domain.SessionMarketClosed {
		t.Fatalf("Quotes() = %+v, %v; want market_closed from the expired calendar, not unknown", batch, err)
	}
}

// A calendar that failed is not asked again for every request.
func TestFailedCalendarIsNotAskedAgainAtOnce(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 503, `{}` }, aaplBars("123.45"))
	if _, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f)); err != nil {
		t.Fatalf("first Quotes() error = %v", err)
	}
	first := f.calendar.hits.Load()

	f.clock.advance(5 * time.Second)
	if _, err := f.prices.Quotes(t.Context(), symbols(t, "MSFT"), cutoffOf(f)); err != nil {
		t.Fatalf("second Quotes() error = %v", err)
	}

	if first != 1 || f.calendar.hits.Load() != first {
		t.Errorf("calendar hits = %d after the first call and %d after the second; want 1 and 1 (no retry, no new ask within the pause)", first, f.calendar.hits.Load())
	}
}

// When the page limit ends the read before every symbol was reached, a symbol without a bar was never
// answered about: it is in neither list, so the valuation says provider_unavailable, not no_price.
func TestSymbolsBeyondThePageLimitAreNotAnsweredAbout(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, func(url.Values) (int, string) {
		return 200, `{"bars":{"AAPL":[{"t":"2026-11-03T14:44:00Z","c":123.45}]},"next_page_token":"more"}`
	})

	batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL", "MSFT"), cutoffOf(f))

	if err != nil || batch.Quotes["AAPL"].Price.IsZero() {
		t.Fatalf("Quotes() = %+v, %v; want AAPL priced", batch, err)
	}
	if _, priced := batch.Quotes["MSFT"]; priced || len(batch.Unavailable) != 0 {
		t.Errorf("Quotes() = %+v; want MSFT in neither Quotes nor Unavailable (the read stopped before it)", batch)
	}
	if got := f.bars.hits.Load(); got != barsMaxPages {
		t.Errorf("bars requests = %d, want %d", got, barsMaxPages)
	}
}
