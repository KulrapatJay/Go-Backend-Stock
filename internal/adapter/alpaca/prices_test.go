package alpaca

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

const oneSessionCalendar = `{"calendar":[{"date":"2026-11-03","core_start":"2026-11-03T14:30:00Z","core_end":"2026-11-03T21:00:00Z"}]}`

// pricesFixture is a Prices over a fake Trading API (calendar) and a fake Market Data API (bars).
type pricesFixture struct {
	prices   *Prices
	calendar *fakeAlpaca
	bars     *barsServer
	clock    *clock
}

func newPricesFixture(t *testing.T, calendar func() (int, string), bars func(url.Values) (int, string)) pricesFixture {
	t.Helper()
	f := pricesFixture{clock: &clock{now: at(2026, 11, 3, 15, 0, 10)}}
	f.calendar = newFakeAlpaca(t, func(w http.ResponseWriter, r *http.Request) {
		status, body := calendar()
		answerJSON(status, body)(w, r)
	})
	f.bars = newBarsServer(t, bars)
	p, err := NewPrices(PricesConfig{
		DataBaseURL: f.bars.URL, TradingBaseURL: f.calendar.URL, KeyID: testKeyID, Secret: testSecret,
		Timeout: 2 * time.Second, Now: f.clock.Now,
	})
	if err != nil {
		t.Fatalf("NewPrices() error = %v", err)
	}
	f.prices = p
	return f
}

func aaplBars(closePrice string) func(url.Values) (int, string) {
	return func(url.Values) (int, string) {
		return 200, `{"bars":{"AAPL":[{"t":"2026-11-03T14:44:00Z","c":` + closePrice + `},{"t":"2026-11-03T14:43:00Z","c":1}]},"next_page_token":null}`
	}
}

func cutoffOf(f pricesFixture) time.Time { return f.clock.now.Add(-15 * time.Minute) } // 14:45:10

func symbols(t *testing.T, raw ...string) []domain.Symbol {
	t.Helper()
	out := make([]domain.Symbol, len(raw))
	for i, r := range raw {
		out[i] = mustSymbol(t, r)
	}
	return out
}

func TestQuotesPricesHeldSymbols(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, aaplBars("189.5"))

	batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL", "ZZZZ"), cutoffOf(f))

	if err != nil {
		t.Fatalf("Quotes() error = %v", err)
	}
	q, ok := batch.Quotes["AAPL"]
	if !ok || q.Price.String() != "189.5" || q.Currency != "USD" || q.Source != "alpaca" || q.Feed != "sip" ||
		q.IsDemo || q.Cached || q.Stale || q.SessionStatus != domain.SessionOpen {
		t.Fatalf("AAPL quote = %+v", q)
	}
	if !q.AsOf.Equal(utc(2026, 11, 3, 14, 45)) || !q.FetchedAt.Equal(f.clock.now) {
		t.Errorf("as_of = %v, fetched_at = %v; want the end of the 14:44 bar and the clock", q.AsOf, q.FetchedAt)
	}
	if _, err := domain.NewPriceQuote(q); err != nil {
		t.Errorf("domain.NewPriceQuote() error = %v", err)
	}
	if len(batch.Unavailable) != 1 || batch.Unavailable[0].String() != "ZZZZ" || len(batch.Quotes) != 1 {
		t.Errorf("batch = %+v, want ZZZZ unavailable and one quote", batch)
	}
	q0 := f.bars.query(0)
	if q0.Get("symbols") != "AAPL,ZZZZ" || q0.Get("start") != "2026-11-03T14:30:00Z" || q0.Get("end") != "2026-11-03T14:44:00Z" {
		t.Errorf("bars query = %v, want the open session up to the last finished minute", q0)
	}
}

func TestQuotesAsksOnceForDuplicateSymbols(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, aaplBars("1"))
	if _, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL", "AAPL"), cutoffOf(f)); err != nil {
		t.Fatal(err)
	}
	if got := f.bars.query(0).Get("symbols"); got != "AAPL" {
		t.Errorf("symbols = %q, want AAPL once", got)
	}
}

func TestBarPricesNeverGoThroughFloat(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, func(url.Values) (int, string) {
		return 200, `{"bars":{
			"AAPL":[{"t":"2026-11-03T14:44:00Z","c":189.8412345678}],
			"MSFT":[{"t":"2026-11-03T14:44:00Z","c":1.5e2}],
			"NVDA":[{"t":"2026-11-03T14:44:00Z","c":0.00000000001}]},"next_page_token":null}`
	})

	batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL", "MSFT", "NVDA"), cutoffOf(f))

	if err != nil {
		t.Fatalf("Quotes() error = %v", err)
	}
	if batch.Quotes["AAPL"].Price.String() != "189.8412345678" {
		t.Errorf("AAPL price = %s, want 189.8412345678 exactly", batch.Quotes["AAPL"].Price)
	}
	for _, s := range []string{"MSFT", "NVDA"} {
		if _, priced := batch.Quotes[s]; priced {
			t.Errorf("%s was priced from a number domain.ParsePrice refuses (exponent or too many decimals); it must be unavailable, never rounded", s)
		}
	}
	if len(batch.Unavailable) != 2 {
		t.Errorf("Unavailable = %v, want MSFT and NVDA", batch.Unavailable)
	}
}

func TestQuotesWithoutACalendarStillPricesAsUnknown(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 500, `` }, aaplBars("189.5"))

	batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f))

	if err != nil {
		t.Fatalf("Quotes() error = %v, want prices even though the calendar is down", err)
	}
	q := batch.Quotes["AAPL"]
	if q.Price.String() != "189.5" || q.SessionStatus != domain.SessionUnknown || !q.Stale {
		t.Errorf("quote = %+v, want unknown and stale without a calendar", q)
	}
	q0 := f.bars.query(0)
	if q0.Get("start") != "2026-10-30T14:45:10Z" || q0.Get("end") != "2026-11-03T14:44:10Z" {
		t.Errorf("bars query = %v, want [cutoff-96h, cutoff-1m] without a calendar", q0)
	}
}

func TestQuotesFailWhenBarsFail(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, func(url.Values) (int, string) { return 403, `{"message":"subscription"}` })

	batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), cutoffOf(f))

	if err == nil || len(batch.Quotes) != 0 {
		t.Fatalf("Quotes() = %+v, %v; want an error and no price at all, never a substitute", batch, err)
	}
	if msg := err.Error(); strings.Contains(msg, testKeyID) || strings.Contains(msg, testSecret) || strings.Contains(msg, "subscription") || !strings.Contains(msg, "403") {
		t.Errorf("error %q leaks a credential or Alpaca's text, or hides the status", msg)
	}
	if errors.Is(err, domain.ErrUnknownSymbol) {
		t.Error("a failed request must not look like an unknown symbol")
	}
}

func TestQuotesAsksForNothingWhenThereIsNothingToAsk(t *testing.T) {
	f := newPricesFixture(t, func() (int, string) { return 200, oneSessionCalendar }, aaplBars("1"))
	if batch, err := f.prices.Quotes(t.Context(), nil, cutoffOf(f)); err != nil || len(batch.Quotes) != 0 || f.bars.hits.Load() != 0 || f.calendar.hits.Load() != 0 {
		t.Errorf("Quotes(no symbols) = %+v, %v with %d bars and %d calendar requests; want none", batch, err, f.bars.hits.Load(), f.calendar.hits.Load())
	}
	// The first minute of the session: no bar has finished, so there is nothing to ask for.
	early := at(2026, 11, 3, 14, 30, 20)
	batch, err := f.prices.Quotes(t.Context(), symbols(t, "AAPL"), early)
	if err != nil || len(batch.Quotes) != 0 || len(batch.Unavailable) != 1 || f.bars.hits.Load() != 0 {
		t.Errorf("Quotes(first minute) = %+v, %v with %d bars requests; want AAPL unavailable and no request", batch, err, f.bars.hits.Load())
	}
}

func TestNewPricesValidatesItsConfig(t *testing.T) {
	ok := PricesConfig{DataBaseURL: "https://data.alpaca.markets", TradingBaseURL: "https://paper-api.alpaca.markets",
		KeyID: testKeyID, Secret: testSecret, Timeout: time.Second}
	if _, err := NewPrices(ok); err != nil {
		t.Fatalf("NewPrices(valid) error = %v", err)
	}
	for name, mutate := range map[string]func(*PricesConfig){
		"no data URL":    func(c *PricesConfig) { c.DataBaseURL = "" },
		"no trading URL": func(c *PricesConfig) { c.TradingBaseURL = "" },
		"no key":         func(c *PricesConfig) { c.KeyID = "" },
		"no secret":      func(c *PricesConfig) { c.Secret = "" },
		"no timeout":     func(c *PricesConfig) { c.Timeout = 0 },
	} {
		cfg := ok
		mutate(&cfg)
		_, err := NewPrices(cfg)
		if err == nil || strings.Contains(err.Error(), testKeyID) || strings.Contains(err.Error(), testSecret) {
			t.Errorf("%s: error = %v, want an error without credentials", name, err)
		}
	}
}
