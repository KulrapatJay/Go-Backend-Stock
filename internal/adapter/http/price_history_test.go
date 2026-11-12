package http

import (
	"crypto/rand"
	"encoding/json"
	nethttp "net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func withBars() harnessOption {
	return func(c *harnessConfig) { c.bars = true }
}

// freshSymbol returns a symbol no other test or earlier run has used: the bars are stored in the shared
// test database, so a reused symbol would already be covered and its test would never reach the provider.
func freshSymbol(t *testing.T) string {
	t.Helper()
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	out := []byte("B")
	for _, b := range raw {
		out = append(out, 'A'+b%26)
	}
	return string(out)
}

func barsPath(symbol, from, to string) string {
	q := url.Values{}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	return "/api/v1/instruments/" + symbol + "/bars?" + q.Encode()
}

func (b *browser) getBars(symbol, from, to string) (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, barsPath(symbol, from, to), nil, nil)
}

func decodeBars(t *testing.T, body []byte) BarsResponse {
	t.Helper()
	var out BarsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("bars body %s: %v", body, err)
	}
	return out
}

// day returns the New York date n days before the harness clock.
func (h *authHarness) day(n int) domain.TradingDate {
	return domain.MarketDate(h.clock.Now()).AddDays(-n)
}

func httpBar(t *testing.T, date domain.TradingDate, open, high, low, closePrice, volume string) domain.DailyBar {
	t.Helper()
	price := func(s string) domain.Price {
		p, err := domain.ParsePrice(s)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	v, err := domain.ParseVolume(volume)
	if err != nil {
		t.Fatal(err)
	}
	return domain.DailyBar{Date: date, Open: price(open), High: price(high), Low: price(low), Close: price(closePrice), Volume: v}
}

func (h *authHarness) setBars(symbol string, bars ...domain.DailyBar) {
	h.prices.mu.Lock()
	defer h.prices.mu.Unlock()
	if h.prices.bars == nil {
		h.prices.bars = map[string][]domain.DailyBar{}
	}
	h.prices.bars[symbol] = bars
}

func (h *authHarness) barCalls() []barCall {
	h.prices.mu.Lock()
	defer h.prices.mu.Unlock()
	return slices.Clone(h.prices.barCalls)
}

func TestBarsNeedASession(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b := h.newBrowser(t)
	resp, body := b.getBars("AAPL", "2026-11-02", "2026-11-06")
	if resp.StatusCode != nethttp.StatusUnauthorized || errorCode(t, body) != "unauthenticated" {
		t.Fatalf("GET bars without a session = %d %s, want 401 unauthenticated", resp.StatusCode, body)
	}
	if len(h.barCalls()) != 0 {
		t.Error("the provider was asked without a session")
	}
}

func TestBarsAnswerWithExactStringsOldestFirst(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b, _ := signedIn(t, h)
	symbol := freshSymbol(t)
	d1, d2, d3 := h.day(5), h.day(4), h.day(3)
	h.setBars(symbol,
		httpBar(t, d1, "351.35", "353.87", "346.4605", "348.51", "29910098"),
		httpBar(t, d2, "10", "12", "9.5", "11.25", "100"),
		httpBar(t, d3, "11.25", "13", "11", "12", "0"))

	resp, body := b.getBars(strings.ToLower(symbol), h.day(8).String(), h.day(1).String())
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET bars = %d %s", resp.StatusCode, body)
	}
	got := decodeBars(t, body)
	if got.Symbol != symbol || got.InstrumentName != "Test Corp" || got.Timeframe != "1d" || got.Adjustment != "raw" ||
		got.Currency != "USD" || got.Source != "alpaca" || got.Feed != "sip" || got.IsDemo {
		t.Errorf("response = %+v", got)
	}
	if len(got.Data) != 3 || got.Data[0].Date != d1.String() || got.Data[1].Date != d2.String() || got.Data[2].Date != d3.String() {
		t.Fatalf("data = %+v, want the three days oldest first", got.Data)
	}
	first := got.Data[0]
	if first.Open != "351.35" || first.High != "353.87" || first.Low != "346.4605" || first.Close != "348.51" || first.Volume != "29910098" {
		t.Errorf("first bar = %+v, want exact decimal strings", first)
	}
	if got.Data[2].Volume != "0" {
		t.Errorf("volume = %q, want 0", got.Data[2].Volume)
	}
	if got.DataFrom == nil || *got.DataFrom != h.day(8).String() || got.DataThrough == nil || *got.DataThrough != h.day(1).String() {
		t.Errorf("data range = %v..%v, want %s..%s", got.DataFrom, got.DataThrough, h.day(8), h.day(1))
	}

	// Every number is a JSON string, never a number.
	raw := rawObject(t, body)
	bar := raw["data"].([]any)[0].(map[string]any)
	for _, field := range []string{"date", "open", "high", "low", "close", "volume"} {
		if _, isString := bar[field].(string); !isString {
			t.Errorf("data[0].%s = %v (%T), want a string", field, bar[field], bar[field])
		}
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	head, headBody := b.do(nethttp.MethodHead, barsPath(symbol, h.day(8).String(), h.day(1).String()), nil, nil)
	if head.StatusCode != nethttp.StatusOK || len(headBody) != 0 {
		t.Errorf("HEAD bars = %d with %d body bytes, want 200 and none", head.StatusCode, len(headBody))
	}
}

func TestBarsAreStoredSoTheSecondAskDoesNotReachTheProvider(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b, _ := signedIn(t, h)
	symbol := freshSymbol(t)
	h.setBars(symbol, httpBar(t, h.day(3), "10", "12", "9", "11", "100"))

	first, firstBody := b.getBars(symbol, h.day(8).String(), h.day(1).String())
	second, secondBody := b.getBars(symbol, h.day(6).String(), h.day(2).String())
	if first.StatusCode != nethttp.StatusOK || second.StatusCode != nethttp.StatusOK {
		t.Fatalf("statuses = %d, %d (%s / %s)", first.StatusCode, second.StatusCode, firstBody, secondBody)
	}
	if calls := h.barCalls(); len(calls) != 1 {
		t.Fatalf("the provider was asked %d times, want once: the second range was already covered", len(calls))
	}
	if got := decodeBars(t, secondBody); len(got.Data) != 1 || got.Data[0].Date != h.day(3).String() || got.Data[0].Close != "11" {
		t.Errorf("second answer = %+v, want the stored bar", got.Data)
	}

	// Another user asks for the same stock: the stored bars are market data shared by everyone.
	other, _ := signedIn(t, h)
	resp, body := other.getBars(symbol, h.day(8).String(), h.day(1).String())
	if resp.StatusCode != nethttp.StatusOK || len(decodeBars(t, body).Data) != 1 || len(h.barCalls()) != 1 {
		t.Errorf("another user = %d %s after %d provider calls, want the shared stored bars", resp.StatusCode, body, len(h.barCalls()))
	}
}

func TestBarsNeverIncludeADayThatHasNotClosed(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b, _ := signedIn(t, h)
	symbol := freshSymbol(t)
	today := h.day(0)
	h.setBars(symbol, httpBar(t, h.day(1), "10", "12", "9", "11", "100"), httpBar(t, today, "11", "13", "10", "12", "50"))

	resp, body := b.getBars(symbol, h.day(3).String(), today.AddDays(5).String())
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET bars = %d %s", resp.StatusCode, body)
	}
	got := decodeBars(t, body)
	if len(got.Data) != 1 || got.Data[0].Date != h.day(1).String() {
		t.Errorf("data = %+v, want only yesterday", got.Data)
	}
	calls := h.barCalls()
	if len(calls) != 1 || calls[0].to.String() != h.day(1).String() {
		t.Errorf("provider calls = %+v, want one that stops at yesterday", calls)
	}
}

func TestBarsOfARangeWithNothingClosedYetIsEmptyAndAsksNothing(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b, _ := signedIn(t, h)
	symbol := freshSymbol(t)

	resp, body := b.getBars(symbol, h.day(0).String(), h.day(0).AddDays(3).String())
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET bars = %d %s", resp.StatusCode, body)
	}
	got := decodeBars(t, body)
	if len(got.Data) != 0 || got.DataFrom != nil || got.DataThrough != nil {
		t.Errorf("response = %+v, want no data and a null data range", got)
	}
	if !strings.Contains(string(body), `"data":[]`) {
		t.Errorf("body = %s, want data as an empty list, never null", body)
	}
	if len(h.barCalls()) != 0 {
		t.Errorf("the provider was asked %d times about days that have not closed", len(h.barCalls()))
	}
}

func TestBarsRefuseABadRequestWith400(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b, _ := signedIn(t, h)
	symbol := freshSymbol(t)
	cases := map[string]string{
		"no from":                    barsPath(symbol, "", "2026-11-06"),
		"no to":                      barsPath(symbol, "2026-11-02", ""),
		"from with a time":           "/api/v1/instruments/" + symbol + "/bars?from=2026-11-02T00:00:00Z&to=2026-11-06",
		"from in another notation":   barsPath(symbol, "11/02/2026", "2026-11-06"),
		"a date that does not exist": barsPath(symbol, "2026-02-30", "2026-03-06"),
		"to before from":             barsPath(symbol, "2026-11-06", "2026-11-02"),
		"more than 3,660 days":       barsPath(symbol, "2016-01-01", "2026-02-01"),
		"a symbol that is no symbol": barsPath("1-bad", "2026-11-02", "2026-11-06"),
		"a symbol with a space":      barsPath("A%20B", "2026-11-02", "2026-11-06"),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			resp, body := b.do(nethttp.MethodGet, path, nil, nil)
			if resp.StatusCode != nethttp.StatusBadRequest || errorCode(t, body) != "invalid_input" {
				t.Fatalf("GET %s = %d %s, want 400 invalid_input", path, resp.StatusCode, body)
			}
		})
	}
	if len(h.barCalls()) != 0 {
		t.Errorf("the provider was asked %d times about a bad request", len(h.barCalls()))
	}
}

func TestBarsAcceptTheLongestRangeAndASymbolWithADot(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b, _ := signedIn(t, h)
	from := domain.NewTradingDate(2016, time.January, 4)
	resp, body := b.getBars(freshSymbol(t)+".B", from.String(), from.AddDays(domain.MaxBarRangeDays-1).String())
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET bars over %d days for a symbol with a dot = %d %s, want 200", domain.MaxBarRangeDays, resp.StatusCode, body)
	}
}

func TestBarsOfAnUnknownSymbolIs422(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b, _ := signedIn(t, h)
	resp, body := b.getBars("UNKNOWNX", h.day(5).String(), h.day(1).String())
	if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "unknown_symbol" {
		t.Fatalf("GET bars = %d %s, want 422 unknown_symbol", resp.StatusCode, body)
	}
	if len(h.barCalls()) != 0 {
		t.Error("bars were asked for a symbol that does not exist")
	}
}

func TestBarsNeedingAnInstrumentCheckSurviveAProviderThatIsDown(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b, _ := signedIn(t, h)
	h.instruments.setDown(true)
	resp, body := b.getBars(freshSymbol(t), h.day(5).String(), h.day(1).String())
	if resp.StatusCode != nethttp.StatusServiceUnavailable || errorCode(t, body) != "dependency_unavailable" {
		t.Fatalf("GET bars = %d %s, want 503 dependency_unavailable", resp.StatusCode, body)
	}
}

func TestBarsOf31NewSymbolsInAnHourHitTheLookupLimit(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b, _ := signedIn(t, h)
	for i := range 30 {
		if resp, body := b.getBars(freshSymbol(t), h.day(5).String(), h.day(1).String()); resp.StatusCode != nethttp.StatusOK {
			t.Fatalf("new symbol %d = %d %s, want 200", i+1, resp.StatusCode, body)
		}
	}
	resp, body := b.getBars(freshSymbol(t), h.day(5).String(), h.day(1).String())
	if resp.StatusCode != nethttp.StatusTooManyRequests || errorCode(t, body) != "rate_limited" {
		t.Fatalf("31st new symbol = %d %s, want 429 rate_limited", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 has no Retry-After")
	}
	// A symbol already stored costs nothing: it is still served at the limit.
	known := freshSymbol(t)
	other, _ := signedIn(t, h)
	if resp, body := other.getBars(known, h.day(5).String(), h.day(1).String()); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("another user's new symbol = %d %s", resp.StatusCode, body)
	}
	if resp, body := b.getBars(known, h.day(5).String(), h.day(1).String()); resp.StatusCode != nethttp.StatusOK {
		t.Errorf("a stored symbol at the limit = %d %s, want 200", resp.StatusCode, body)
	}
}

func TestBarsWhenTheProviderIsDown(t *testing.T) {
	h := newAuthHarness(t, withBars())
	b, _ := signedIn(t, h)
	symbol := freshSymbol(t)
	h.setBars(symbol, httpBar(t, h.day(6), "10", "12", "9", "11", "100"))

	h.prices.mu.Lock()
	h.prices.barsDown = true
	h.prices.mu.Unlock()
	resp, body := b.getBars(symbol, h.day(8).String(), h.day(1).String())
	if resp.StatusCode != nethttp.StatusServiceUnavailable || errorCode(t, body) != "dependency_unavailable" {
		t.Fatalf("nothing stored, provider down = %d %s, want 503 dependency_unavailable", resp.StatusCode, body)
	}

	h.prices.mu.Lock()
	h.prices.barsDown = false
	h.prices.mu.Unlock()
	if resp, body := b.getBars(symbol, h.day(8).String(), h.day(3).String()); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("provider back = %d %s", resp.StatusCode, body)
	}

	h.prices.mu.Lock()
	h.prices.barsDown = true
	h.prices.mu.Unlock()
	resp, body = b.getBars(symbol, h.day(8).String(), h.day(1).String())
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("something stored, provider down = %d %s, want the stored bars with 200", resp.StatusCode, body)
	}
	got := decodeBars(t, body)
	if len(got.Data) != 1 || got.DataThrough == nil || *got.DataThrough != h.day(3).String() {
		t.Errorf("response = %+v / through %v, want the stored bar and data_through %s so the client sees the gap", got.Data, got.DataThrough, h.day(3))
	}
}
