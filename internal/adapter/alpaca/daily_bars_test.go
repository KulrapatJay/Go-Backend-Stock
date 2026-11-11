package alpaca

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// dailyFixture is a Prices over a fake Market Data API; the calendar is not used by DailyBars, so its
// URL only has to be present.
type dailyFixture struct {
	prices *Prices
	bars   *barsServer
	clock  *clock
}

// newDailyFixture sets the clock to now: by default 2026-11-11 15:00 UTC, which is 10:00 in New York on
// Wednesday 11 November, so the last closed day is Tuesday 10 November.
func newDailyFixture(t *testing.T, now time.Time, respond func(url.Values) (int, string)) dailyFixture {
	t.Helper()
	f := dailyFixture{clock: &clock{now: now}, bars: newBarsServer(t, respond)}
	p, err := NewPrices(PricesConfig{
		DataBaseURL: f.bars.URL, TradingBaseURL: "http://127.0.0.1:1", KeyID: testKeyID, Secret: testSecret,
		Timeout: 2 * time.Second, Now: f.clock.Now,
	})
	if err != nil {
		t.Fatalf("NewPrices() error = %v", err)
	}
	f.prices = p
	return f
}

var wednesdayMorning = time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)

func tradingDate(t *testing.T, s string) domain.TradingDate {
	t.Helper()
	d, err := domain.ParseTradingDate("date", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func aaplDailyBar(ts string) string {
	return `{"c":348.51,"h":353.87,"l":346.4605,"n":637158,"o":351.35,"t":"` + ts + `","v":29910098,"vw":348.62858}`
}

func TestDailyBarsAsksForRawSIPDailyBarsOldestFirst(t *testing.T) {
	f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) {
		return 200, `{"bars":{"AAPL":[` + aaplDailyBar("2026-11-09T05:00:00Z") + `]},"next_page_token":null}`
	})

	batch, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL", "MSFT"), tradingDate(t, "2026-11-02"), tradingDate(t, "2026-11-10"))
	if err != nil {
		t.Fatalf("DailyBars() error = %v", err)
	}

	q := f.bars.query(0)
	want := map[string]string{
		"symbols": "AAPL,MSFT", "timeframe": "1Day", "feed": "sip", "adjustment": "raw", "sort": "asc", "limit": "10000",
		"start": "2026-11-02T05:00:00Z", // midnight New York, EST
		"end":   "2026-11-11T04:59:59Z", // the last second of 10 November in New York
	}
	for key, value := range want {
		if q.Get(key) != value {
			t.Errorf("query %s = %q, want %q", key, q.Get(key), value)
		}
	}
	if f.bars.path.Load() != "/v2/stocks/bars" || f.bars.keyID.Load() != testKeyID || f.bars.secret.Load() != testSecret {
		t.Errorf("path = %v, key = %v, secret = %v", f.bars.path.Load(), f.bars.keyID.Load(), f.bars.secret.Load())
	}
	if batch.Source != domain.BarSourceAlpaca || batch.Feed != "sip" || batch.IsDemo() {
		t.Errorf("batch source/feed = %s/%s, demo %v; want alpaca/sip, not demo", batch.Source, batch.Feed, batch.IsDemo())
	}
}

func TestDailyBarsNeverAsksForDataNewerThanTheDelay(t *testing.T) {
	// 00:10 in New York on 11 November: the end of 10 November is 14 minutes ago, newer than the 15-minute
	// delay, and Alpaca answers 403 to an end that recent.
	f := newDailyFixture(t, time.Date(2026, 11, 11, 5, 10, 0, 0, time.UTC), func(url.Values) (int, string) {
		return 200, `{"bars":{},"next_page_token":null}`
	})

	if _, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-02"), tradingDate(t, "2026-11-10")); err != nil {
		t.Fatalf("DailyBars() error = %v", err)
	}
	if got := f.bars.query(0).Get("end"); got != "2026-11-11T04:55:00Z" {
		t.Errorf("end = %q, want 2026-11-11T04:55:00Z (now minus 15 minutes)", got)
	}
}

func TestDailyBarsDropsTheBarOfADayThatHasNotClosed(t *testing.T) {
	// to is today and Alpaca (wrongly) sends today's partial bar and a bar from the future.
	f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) {
		return 200, `{"bars":{"AAPL":[` + aaplDailyBar("2026-11-10T05:00:00Z") + `,` + aaplDailyBar("2026-11-11T05:00:00Z") + `,` +
			aaplDailyBar("2026-11-12T05:00:00Z") + `]},"next_page_token":null}`
	})

	batch, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-09"), tradingDate(t, "2026-11-13"))
	if err != nil {
		t.Fatalf("DailyBars() error = %v", err)
	}
	got := batch.Bars["AAPL"]
	if len(got) != 1 || got[0].Date.String() != "2026-11-10" {
		t.Fatalf("bars = %v, want only 2026-11-10 (today and later have not closed)", got)
	}
	if end := f.bars.query(0).Get("end"); end != "2026-11-11T04:59:59Z" {
		t.Errorf("end = %q, want it clamped to the end of the last closed day", end)
	}
}

func TestDailyBarsDoesNotAskWhenNothingInTheRangeHasClosed(t *testing.T) {
	f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) {
		return 200, `{"bars":{},"next_page_token":null}`
	})

	batch, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-11"), tradingDate(t, "2026-11-20"))
	if err != nil || len(batch.Bars["AAPL"]) != 0 {
		t.Fatalf("DailyBars() = %v, %v; want no bars and no error", batch.Bars, err)
	}
	if f.bars.hits.Load() != 0 {
		t.Errorf("Alpaca was asked %d times about a range that has not closed", f.bars.hits.Load())
	}
}

func TestDailyBarsDatesABarByTheNewYorkDay(t *testing.T) {
	f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) {
		return 200, `{"bars":{"AAPL":[` + aaplDailyBar("2026-03-06T05:00:00Z") + `,` + aaplDailyBar("2026-07-01T04:00:00Z") + `]},"next_page_token":null}`
	})

	batch, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-03-01"), tradingDate(t, "2026-07-31"))
	if err != nil || len(batch.Bars["AAPL"]) != 2 {
		t.Fatalf("DailyBars() = %v, %v", batch.Bars, err)
	}
	if a, b := batch.Bars["AAPL"][0].Date.String(), batch.Bars["AAPL"][1].Date.String(); a != "2026-03-06" || b != "2026-07-01" {
		t.Errorf("dates = %s, %s; want 2026-03-06 (EST) and 2026-07-01 (EDT)", a, b)
	}
	// 2026-03-06 starts at 05:00Z and 2026-07-01 at 04:00Z: the request spans the zone change.
	if start := f.bars.query(0).Get("start"); start != "2026-03-01T05:00:00Z" {
		t.Errorf("start = %q, want 2026-03-01T05:00:00Z", start)
	}
	if end := f.bars.query(0).Get("end"); end != "2026-08-01T03:59:59Z" {
		t.Errorf("end = %q, want 2026-08-01T03:59:59Z (31 July ends at 04:00Z minus a second in EDT)", end)
	}
}

func TestDailyBarsKeepsPricesAndVolumeExact(t *testing.T) {
	f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) {
		return 200, `{"bars":{"AAPL":[{"t":"2026-11-10T05:00:00Z","o":351.35,"h":353.8712345678,"l":346.4605,"c":348.51,"v":29910098}]},"next_page_token":null}`
	})

	batch, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-10"), tradingDate(t, "2026-11-10"))
	if err != nil || len(batch.Bars["AAPL"]) != 1 {
		t.Fatalf("DailyBars() = %v, %v", batch.Bars, err)
	}
	b := batch.Bars["AAPL"][0]
	if b.Open.String() != "351.35" || b.High.String() != "353.8712345678" || b.Low.String() != "346.4605" ||
		b.Close.String() != "348.51" || b.Volume.String() != "29910098" {
		t.Errorf("bar = %s %s %s %s %s; numbers must not pass through a float", b.Open, b.High, b.Low, b.Close, b.Volume)
	}
}

func TestDailyBarsGivesASymbolWithoutBarsAnEmptyListNotAnError(t *testing.T) {
	f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) {
		return 200, `{"bars":{"AAPL":[` + aaplDailyBar("2026-11-10T05:00:00Z") + `]},"next_page_token":null}`
	})

	batch, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL", "ARM", "NOPE"), tradingDate(t, "2026-11-10"), tradingDate(t, "2026-11-10"))
	if err != nil {
		t.Fatalf("DailyBars() error = %v", err)
	}
	if len(batch.Bars["AAPL"]) != 1 || len(batch.Bars["ARM"]) != 0 || len(batch.Bars["NOPE"]) != 0 {
		t.Errorf("bars = %v, want one AAPL bar and nothing for ARM and NOPE", batch.Bars)
	}
}

func TestDailyBarsAsksOnceForSeveralSymbolsAndIgnoresRepeats(t *testing.T) {
	f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) { return 200, `{"bars":{},"next_page_token":null}` })

	if _, err := f.prices.DailyBars(t.Context(), symbols(t, "SPY", "QQQ", "SPY"), tradingDate(t, "2026-11-10"), tradingDate(t, "2026-11-10")); err != nil {
		t.Fatal(err)
	}
	if f.bars.hits.Load() != 1 || f.bars.query(0).Get("symbols") != "SPY,QQQ" {
		t.Errorf("requests = %d, symbols = %q; want one request for SPY,QQQ", f.bars.hits.Load(), f.bars.query(0).Get("symbols"))
	}

	f2 := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) { return 200, `{}` })
	if batch, err := f2.prices.DailyBars(t.Context(), nil, tradingDate(t, "2026-11-10"), tradingDate(t, "2026-11-10")); err != nil || f2.bars.hits.Load() != 0 || len(batch.Bars) != 0 {
		t.Errorf("no symbols: batch %v, err %v, requests %d; want nothing asked", batch.Bars, err, f2.bars.hits.Load())
	}
}

func TestDailyBarsFollowsPagesAndFailsWhenThePageLimitCutsTheAnswer(t *testing.T) {
	t.Run("merges pages oldest first", func(t *testing.T) {
		f := newDailyFixture(t, wednesdayMorning, func(q url.Values) (int, string) {
			if q.Get("page_token") == "" {
				return 200, `{"bars":{"AAPL":[` + aaplDailyBar("2026-11-05T05:00:00Z") + `,` + aaplDailyBar("2026-11-06T05:00:00Z") + `]},"next_page_token":"p2"}`
			}
			return 200, `{"bars":{"AAPL":[` + aaplDailyBar("2026-11-09T05:00:00Z") + `]},"next_page_token":null}`
		})
		batch, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-01"), tradingDate(t, "2026-11-10"))
		if err != nil || len(batch.Bars["AAPL"]) != 3 || f.bars.hits.Load() != 2 {
			t.Fatalf("DailyBars() = %d bars, %v after %d requests; want 3 after 2", len(batch.Bars["AAPL"]), err, f.bars.hits.Load())
		}
		if f.bars.query(1).Get("page_token") != "p2" {
			t.Errorf("second request page_token = %q, want p2", f.bars.query(1).Get("page_token"))
		}
		if batch.Bars["AAPL"][2].Date.String() != "2026-11-09" {
			t.Errorf("last bar = %s, want 2026-11-09", batch.Bars["AAPL"][2].Date)
		}
	})
	t.Run("an answer cut by the page limit is an error, never a short list", func(t *testing.T) {
		// A short list would be saved as covering the whole range and the missing days would never be fetched.
		f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) {
			return 200, `{"bars":{"AAPL":[` + aaplDailyBar("2026-11-05T05:00:00Z") + `]},"next_page_token":"more"}`
		})
		if _, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-01"), tradingDate(t, "2026-11-10")); err == nil {
			t.Fatal("DailyBars() succeeded with pages left unread, want an error")
		}
		if f.bars.hits.Load() != dailyBarsMaxPages {
			t.Errorf("requests = %d, want %d", f.bars.hits.Load(), dailyBarsMaxPages)
		}
	})
}

func TestDailyBarsKeepsOneBarPerDay(t *testing.T) {
	f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) {
		return 200, `{"bars":{"AAPL":[` + aaplDailyBar("2026-11-09T05:00:00Z") + `,` + aaplDailyBar("2026-11-09T05:00:00Z") + `]},"next_page_token":null}`
	})
	batch, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-09"), tradingDate(t, "2026-11-09"))
	if err != nil || len(batch.Bars["AAPL"]) != 1 {
		t.Fatalf("DailyBars() = %v, %v; want one bar", batch.Bars, err)
	}
}

func TestDailyBarsRefusesAnAnswerItCannotStoreExactly(t *testing.T) {
	cases := map[string]string{
		"an exponent in a price":      `{"t":"2026-11-10T05:00:00Z","o":1.5e2,"h":160,"l":100,"c":150,"v":10}`,
		"more than ten decimals":      `{"t":"2026-11-10T05:00:00Z","o":1.00000000001,"h":2,"l":1,"c":1.5,"v":10}`,
		"a zero price":                `{"t":"2026-11-10T05:00:00Z","o":0,"h":2,"l":1,"c":1.5,"v":10}`,
		"high below low":              `{"t":"2026-11-10T05:00:00Z","o":1,"h":1,"l":2,"c":1,"v":10}`,
		"a fractional volume":         `{"t":"2026-11-10T05:00:00Z","o":1,"h":2,"l":1,"c":1.5,"v":10.5}`,
		"a missing price":             `{"t":"2026-11-10T05:00:00Z","o":1,"h":2,"c":1.5,"v":10}`,
		"a missing volume":            `{"t":"2026-11-10T05:00:00Z","o":1,"h":2,"l":1,"c":1.5}`,
		"a timestamp that is no time": `{"t":"yesterday","o":1,"h":2,"l":1,"c":1.5,"v":10}`,
	}
	for name, bar := range cases {
		t.Run(name, func(t *testing.T) {
			// Skipping the bar would record its day as fetched with no data; the whole answer is refused instead.
			f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) {
				return 200, `{"bars":{"AAPL":[` + bar + `]},"next_page_token":null}`
			})
			if _, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-10"), tradingDate(t, "2026-11-10")); err == nil {
				t.Fatal("DailyBars() succeeded, want an error")
			}
		})
	}
	for name, body := range map[string]string{"not JSON": `<html>`, "JSON without bars": `{"message":"ok"}`, "bars null": `{"bars":null}`} {
		t.Run("an answer that is "+name, func(t *testing.T) {
			f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) { return 200, body })
			if _, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-10"), tradingDate(t, "2026-11-10")); err == nil {
				t.Fatal("DailyBars() succeeded, want an error")
			}
		})
	}
}

func TestDailyBarsFailsWhenAlpacaCannotBeAskedAndLeaksNoSecret(t *testing.T) {
	for _, code := range []int{401, 403, 404, 422, 429, 500, 503} {
		f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) {
			return code, `{"message":"` + testSecret + ` ` + testKeyID + `"}`
		})
		_, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-10"), tradingDate(t, "2026-11-10"))
		if err == nil {
			t.Errorf("status %d: DailyBars() succeeded, want an error", code)
			continue
		}
		if msg := err.Error(); strings.Contains(msg, testSecret) || strings.Contains(msg, testKeyID) || strings.Contains(msg, "message") {
			t.Errorf("status %d: error %q repeats what Alpaca or the config said", code, msg)
		}
	}
}

func TestDailyBarsStopsWhenTheContextIsCancelled(t *testing.T) {
	f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) { return 200, `{"bars":{}}` })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.prices.DailyBars(ctx, symbols(t, "AAPL"), tradingDate(t, "2026-11-10"), tradingDate(t, "2026-11-10")); err == nil {
		t.Fatal("DailyBars() succeeded with a cancelled context")
	}
}

func TestDailyBarsRefusesAnImpossibleRange(t *testing.T) {
	f := newDailyFixture(t, wednesdayMorning, func(url.Values) (int, string) { return 200, `{"bars":{}}` })
	if _, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), tradingDate(t, "2026-11-10"), tradingDate(t, "2026-11-01")); err == nil {
		t.Error("from after to: DailyBars() succeeded, want an error")
	}
	if _, err := f.prices.DailyBars(t.Context(), symbols(t, "AAPL"), domain.TradingDate{}, tradingDate(t, "2026-11-01")); err == nil {
		t.Error("no from: DailyBars() succeeded, want an error")
	}
	if f.bars.hits.Load() != 0 {
		t.Errorf("Alpaca was asked %d times about an impossible range", f.bars.hits.Load())
	}
}
