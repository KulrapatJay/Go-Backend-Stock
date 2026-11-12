package http

import (
	"encoding/json"
	nethttp "net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// withPerformance serves the performance routes with portfolios and trades, over a fake price provider and
// a fake instrument provider the test controls.
func withPerformance() harnessOption {
	return func(c *harnessConfig) { c.portfolios, c.trades, c.performance = true, true, true }
}

const accountPerformancePath = "/api/v1/performance"

func performancePath(portfolioID, query string) string {
	return "/api/v1/portfolios/" + portfolioID + "/performance?" + query
}

func (b *browser) getPerformance(portfolioID, query string) (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, performancePath(portfolioID, query), nil, nil)
}

func (b *browser) getAccountPerformance(query string) (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, accountPerformancePath+"?"+query, nil, nil)
}

func decodePerformance(t *testing.T, body []byte) PerformanceResponse {
	t.Helper()
	var out PerformanceResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("performance body %s: %v", body, err)
	}
	return out
}

// atNoon is how long ago it was noon in New York, n days before the harness clock: a trade made then is
// dated that day whatever the hour is now.
func (h *authHarness) atNoon(n int) time.Duration {
	return h.clock.Now().Sub(h.day(n).MarketDayStart().Add(12 * time.Hour))
}

// setBenchmarks gives SPY and QQQ the same three days in every test. The bars are stored in the shared test
// database, so a test that asked for a benchmark without them would leave a day recorded as empty for the
// others.
func (h *authHarness) setBenchmarks(t *testing.T) {
	t.Helper()
	for symbol, closes := range map[string][3]string{"SPY": {"500", "505", "510"}, "QQQ": {"400", "404", "408"}} {
		h.setBars(symbol,
			httpBar(t, h.day(3), closes[0], closes[0], closes[0], closes[0], "1"),
			httpBar(t, h.day(2), closes[1], closes[1], closes[1], closes[1], "1"),
			httpBar(t, h.day(1), closes[2], closes[2], closes[2], closes[2], "1"))
	}
}

func (h *authHarness) setCloses(t *testing.T, symbol string, closes map[int]string) {
	t.Helper()
	var bars []domain.DailyBar
	for _, n := range []int{3, 2, 1} {
		if c, ok := closes[n]; ok {
			bars = append(bars, httpBar(t, h.day(n), c, c, c, c, "1"))
		}
	}
	h.setBars(symbol, bars...)
}

func TestPerformanceNeedsASession(t *testing.T) {
	h := newAuthHarness(t, withPerformance())
	b := h.newBrowser(t)
	for _, path := range []string{performancePath("0b6b6c3e-3c0e-4b9e-9f3b-6f2f3b1c9d11", "range=1m"), accountPerformancePath + "?range=1m"} {
		resp, body := b.do(nethttp.MethodGet, path, nil, nil)
		if resp.StatusCode != nethttp.StatusUnauthorized || errorCode(t, body) != "unauthenticated" {
			t.Fatalf("GET %s without a session = %d %s, want 401 unauthenticated", path, resp.StatusCode, body)
		}
	}
	if len(h.barCalls()) != 0 {
		t.Error("the provider was asked without a session")
	}
}

func TestPerformanceAnswersExactStringsOldestFirst(t *testing.T) {
	h := newAuthHarness(t, withPerformance())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := freshSymbol(t)
	b.trade(p.ID, symbol, "buy", "10", "100", "0", h.atNoon(3))
	h.setCloses(t, symbol, map[int]string{3: "100", 2: "110", 1: "121"})
	h.setBenchmarks(t)

	resp, body := b.getPerformance(p.ID, "range=1m&benchmarks=SPY")
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET performance = %d %s", resp.StatusCode, body)
	}
	got := decodePerformance(t, body)
	if got.Method != "time_weighted" || !got.FeesIncluded || got.Adjustment != "raw" || got.Interval != "1d" ||
		got.Currency != "USD" || got.Coverage != "complete" || got.Source != "alpaca" || got.IsDemo {
		t.Errorf("response = %+v", got)
	}
	if got.Range.From == nil || *got.Range.From != h.day(3).String() || got.Range.To == nil || *got.Range.To != h.day(1).String() {
		t.Errorf("range = %+v, want %s..%s", got.Range, h.day(3), h.day(1))
	}
	if got.DataThrough == nil || *got.DataThrough != h.day(1).String() {
		t.Errorf("data_through = %v, want %s", got.DataThrough, h.day(1))
	}
	pts := got.Portfolio.Points
	if len(pts) != 3 || pts[0].Date != *got.Range.From {
		t.Fatalf("points = %+v, want the three days, the first being range.from", pts)
	}
	for i, want := range [][2]string{{"1000", "0"}, {"1100", "10"}, {"1210", "21"}} {
		if pts[i].MarketValue == nil || *pts[i].MarketValue != want[0] || pts[i].ReturnPercent == nil || *pts[i].ReturnPercent != want[1] {
			t.Errorf("point %d = %+v, want market_value %s return_percent %s", i, pts[i], want[0], want[1])
		}
	}
	if got.Portfolio.ReturnPercent == nil || *got.Portfolio.ReturnPercent != "21" {
		t.Errorf("portfolio.return_percent = %v, want 21", got.Portfolio.ReturnPercent)
	}
	if len(got.Benchmarks) != 1 {
		t.Fatalf("benchmarks = %+v", got.Benchmarks)
	}
	spy := got.Benchmarks[0]
	if spy.Symbol != "SPY" || spy.Label != "S&P 500 (SPY)" || spy.ReturnPercent == nil || *spy.ReturnPercent != "2" ||
		spy.ExcessReturnPercent == nil || *spy.ExcessReturnPercent != "19" || len(spy.Points) != 3 {
		t.Errorf("SPY = %+v, want 2%% and an excess of 19", spy)
	}
	for i, want := range []string{"0", "1", "2"} {
		if spy.Points[i].ReturnPercent == nil || *spy.Points[i].ReturnPercent != want || spy.Points[i].Date != pts[i].Date {
			t.Errorf("SPY point %d = %+v, want %s on %s", i, spy.Points[i], want, pts[i].Date)
		}
	}

	// Every number is a JSON string, never a number.
	raw := rawObject(t, body)
	point := raw["portfolio"].(map[string]any)["points"].([]any)[0].(map[string]any)
	for _, field := range []string{"date", "market_value", "return_percent"} {
		if _, isString := point[field].(string); !isString {
			t.Errorf("points[0].%s = %v (%T), want a string", field, point[field], point[field])
		}
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	head, headBody := b.do(nethttp.MethodHead, performancePath(p.ID, "range=1m"), nil, nil)
	if head.StatusCode != nethttp.StatusOK || len(headBody) != 0 {
		t.Errorf("HEAD = %d with %d body bytes, want 200 and none", head.StatusCode, len(headBody))
	}
}

func TestPerformanceRefusesABadRequestWith400(t *testing.T) {
	h := newAuthHarness(t, withPerformance())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	cases := map[string]string{
		"no range":                       performancePath(p.ID, ""),
		"an unknown range":               performancePath(p.ID, "range=2y"),
		"a range in capitals":            performancePath(p.ID, "range=1M"),
		"a benchmark that is not":        performancePath(p.ID, "range=1m&benchmarks=AAPL"),
		"a repeated benchmark":           performancePath(p.ID, "range=1m&benchmarks=SPY,SPY"),
		"a benchmark with space":         performancePath(p.ID, "range=1m&benchmarks="+url.QueryEscape("SPY, QQQ")),
		"a portfolio id that is no UUID": performancePath("abc", "range=1m"),
		"the account without a range":    accountPerformancePath,
		"the account with a bad range":   accountPerformancePath + "?range=forever",
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

func TestPerformanceOfAnotherUsersPortfolioIs404(t *testing.T) {
	h := newAuthHarness(t, withPerformance())
	owner, _ := signedIn(t, h)
	theirs := owner.mustCreatePortfolio("Mine", "")
	intruder, _ := signedIn(t, h)

	resp, body := intruder.getPerformance(theirs.ID, "range=1m")
	if resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Fatalf("GET another user's performance = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	missing, missingBody := intruder.getPerformance("0b6b6c3e-3c0e-4b9e-9f3b-6f2f3b1c9d11", "range=1m")
	if missing.StatusCode != resp.StatusCode || errorCode(t, missingBody) != "not_found" {
		t.Errorf("a portfolio that does not exist = %d %s, want the same answer", missing.StatusCode, missingBody)
	}
}

func TestPerformanceOfAnEmptyPortfolioHasEmptyListsNotNull(t *testing.T) {
	h := newAuthHarness(t, withPerformance())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Empty", "")
	resp, body := b.getPerformance(p.ID, "range=1m")
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET performance = %d %s", resp.StatusCode, body)
	}
	got := decodePerformance(t, body)
	if got.Coverage != "complete" || len(got.Portfolio.Points) != 0 || len(got.Benchmarks) != 0 ||
		got.Range.From != nil || got.Range.To != nil || got.Portfolio.ReturnPercent != nil || got.DataThrough != nil {
		t.Errorf("response = %+v, want complete, no points, null range, return and data_through", got)
	}
	for _, want := range []string{`"points":[]`, `"benchmarks":[]`, `"from":null`, `"return_percent":null`, `"data_through":null`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body %s does not contain %s", body, want)
		}
	}
	if len(h.barCalls()) != 0 {
		t.Errorf("the provider was asked %d times for a portfolio that holds nothing", len(h.barCalls()))
	}
}

func TestPerformanceShowsAMissingBarAsNull(t *testing.T) {
	h := newAuthHarness(t, withPerformance())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := freshSymbol(t)
	b.trade(p.ID, symbol, "buy", "10", "100", "0", h.atNoon(3))
	h.setCloses(t, symbol, map[int]string{3: "100", 1: "121"}) // no bar on day 2, the benchmark has one
	h.setBenchmarks(t)

	resp, body := b.getPerformance(p.ID, "range=1m&benchmarks=SPY")
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET performance = %d %s", resp.StatusCode, body)
	}
	got := decodePerformance(t, body)
	if got.Coverage != "partial" || len(got.Portfolio.Points) != 3 {
		t.Fatalf("coverage %s with %d points, want partial with three", got.Coverage, len(got.Portfolio.Points))
	}
	gap := got.Portfolio.Points[1]
	if gap.Date != h.day(2).String() || gap.MarketValue != nil || gap.ReturnPercent != nil {
		t.Errorf("point of the day without a bar = %+v, want null value and return", gap)
	}
	if !strings.Contains(string(body), `"market_value":null`) {
		t.Errorf("body %s must say null for the day without a bar, not 0", body)
	}
	if last := got.Portfolio.Points[2]; last.ReturnPercent == nil || *last.ReturnPercent != "21" {
		t.Errorf("last point = %+v, want the return linked across the gap: 21", last)
	}
}

func TestAccountPerformanceAddsPortfoliosOfTheCallerOnly(t *testing.T) {
	h := newAuthHarness(t, withPerformance())
	b, _ := signedIn(t, h)
	long, short := b.mustCreatePortfolio("Long term", ""), b.mustCreatePortfolio("Short term", "")
	other, _ := signedIn(t, h)
	theirs := other.mustCreatePortfolio("Theirs", "")
	symbol := freshSymbol(t)
	b.trade(long.ID, symbol, "buy", "10", "100", "0", h.atNoon(3))
	b.trade(short.ID, symbol, "buy", "10", "100", "0", h.atNoon(3))
	other.trade(theirs.ID, symbol, "buy", "500", "100", "0", h.atNoon(3))
	h.setCloses(t, symbol, map[int]string{3: "100", 2: "110", 1: "121"})

	resp, body := b.getAccountPerformance("range=1m")
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET %s = %d %s", accountPerformancePath, resp.StatusCode, body)
	}
	got := decodePerformance(t, body)
	if len(got.Portfolio.Points) != 3 || got.Portfolio.Points[0].MarketValue == nil || *got.Portfolio.Points[0].MarketValue != "2000" {
		t.Fatalf("points = %+v, want 20 shares x 100 = 2000 on the first day (nothing of the other user)", got.Portfolio.Points)
	}
	if *got.Portfolio.Points[2].MarketValue != "2420" || *got.Portfolio.ReturnPercent != "21" {
		t.Errorf("last point %+v, return %v; want 2420 and 21", got.Portfolio.Points[2], got.Portfolio.ReturnPercent)
	}
	head, headBody := b.do(nethttp.MethodHead, accountPerformancePath+"?range=1m", nil, nil)
	if head.StatusCode != nethttp.StatusOK || len(headBody) != 0 {
		t.Errorf("HEAD = %d with %d body bytes, want 200 and none", head.StatusCode, len(headBody))
	}
}

func TestPerformanceWhenTheProviderIsDownAndNothingIsStoredIs503(t *testing.T) {
	h := newAuthHarness(t, withPerformance())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	b.trade(p.ID, freshSymbol(t), "buy", "10", "100", "0", h.atNoon(3))
	h.prices.mu.Lock()
	h.prices.barsDown = true
	h.prices.mu.Unlock()

	resp, body := b.getPerformance(p.ID, "range=1m")
	if resp.StatusCode != nethttp.StatusServiceUnavailable || errorCode(t, body) != "dependency_unavailable" {
		t.Fatalf("GET performance = %d %s, want 503 dependency_unavailable", resp.StatusCode, body)
	}
}
