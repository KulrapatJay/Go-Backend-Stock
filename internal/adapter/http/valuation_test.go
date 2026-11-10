package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/port"
)

// goldenTrades is the example of spec section 12: buy 2 x 100 + fee 2, sell 0.5 x 120 - fee 1.
func goldenTrades(b *browser, portfolioID string) {
	b.t.Helper()
	b.trade(portfolioID, "AAPL", "buy", "2", "100", "2", 3*time.Hour)
	b.trade(portfolioID, "AAPL", "sell", "0.5", "120", "1", 2*time.Hour)
}

func TestHoldingsListsOpenAndClosedPositions(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	goldenTrades(b, p.ID)
	b.trade(p.ID, "MSFT", "buy", "3", "10", "0", 90*time.Minute)
	b.trade(p.ID, "MSFT", "sell", "3", "10", "0", 80*time.Minute)

	resp, body := b.getHoldings(p.ID)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET holdings = %d %s", resp.StatusCode, body)
	}
	got := decodeHoldings(t, body)
	if len(got.Data) != 2 || got.Data[0].Symbol != "AAPL" || got.Data[1].Symbol != "MSFT" {
		t.Fatalf("data = %+v, want AAPL then MSFT", got.Data)
	}
	aapl, msft := got.Data[0], got.Data[1]
	if aapl.Quantity != "1.5" || aapl.CostBasis != "151.5" || aapl.AverageCost != "101" || aapl.RealizedPnL != "8.5" || aapl.InstrumentName == "" {
		t.Errorf("AAPL = %+v", aapl)
	}
	if msft.Quantity != "0" || msft.CostBasis != "0" || msft.AverageCost != "0" || msft.RealizedPnL != "0" {
		t.Errorf("MSFT (sold out) = %+v", msft)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if h.prices.callCount() != 0 {
		t.Errorf("holdings asked the price provider %d times, want never", h.prices.callCount())
	}

	head, headBody := b.do(nethttp.MethodHead, holdingsPath(p.ID), nil, nil)
	if head.StatusCode != nethttp.StatusOK || len(headBody) != 0 {
		t.Errorf("HEAD holdings = %d with %d body bytes, want 200 and none", head.StatusCode, len(headBody))
	}
}

func TestValuationGoldenExample(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	goldenTrades(b, p.ID)
	h.prices.setPrice("AAPL", "110")

	resp, body := b.getValuation(p.ID)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET valuation = %d %s", resp.StatusCode, body)
	}
	got := decodeValuation(t, body)
	if got.PortfolioID != p.ID || got.Currency != "USD" || got.Coverage != "complete" ||
		got.MarketValue == nil || *got.MarketValue != "165" || got.UnrealizedPnL == nil || *got.UnrealizedPnL != "13.5" ||
		got.RealizedPnL != "8.5" || got.PricedMarketValue != "165" {
		t.Errorf("valuation = %+v", got)
	}
	if got.MissingSymbols == nil || len(got.MissingSymbols) != 0 || len(got.Positions) != 1 {
		t.Errorf("missing_symbols = %#v, positions = %d; want [] and one position", got.MissingSymbols, len(got.Positions))
	}
	pos := got.Positions[0]
	if pos.Symbol != "AAPL" || pos.Quantity != "1.5" || pos.CostBasis != "151.5" || pos.UnavailableReason != nil ||
		pos.MarketValue == nil || *pos.MarketValue != "165" || pos.UnrealizedPnL == nil || *pos.UnrealizedPnL != "13.5" {
		t.Errorf("position = %+v", pos)
	}
	q := pos.Quote
	if q == nil || q.Price != "110" || q.Currency != "USD" || !q.IsDemo || q.Stale || q.Cached || q.SessionStatus != "unknown" ||
		q.Source != "test" || q.Feed != "test" {
		t.Fatalf("quote = %+v", q)
	}
	// The kind of price comes from the data, so a client need not hard-code "delayed 15 minutes".
	if rawQuote := rawPositions(t, body)["AAPL"]["quote"].(map[string]any); rawQuote["price_type"] != "delayed_bar_close" || rawQuote["delay_minutes"] != float64(15) {
		t.Errorf("price_type = %v, delay_minutes = %v; want delayed_bar_close and 15", rawQuote["price_type"], rawQuote["delay_minutes"])
	}
	if !strings.HasSuffix(rawObject(t, body)["positions"].([]any)[0].(map[string]any)["quote"].(map[string]any)["as_of"].(string), "Z") {
		t.Errorf("quote.as_of is not UTC in %s", body)
	}
	if !got.CalculatedAt.Equal(h.clock.Now()) {
		t.Errorf("calculated_at = %v, want the clock %v", got.CalculatedAt, h.clock.Now())
	}
	h.prices.mu.Lock()
	cutoff := h.prices.cutoffs[0]
	h.prices.mu.Unlock()
	if want := h.clock.Now().Add(-15 * time.Minute); !cutoff.Equal(want) {
		t.Errorf("cutoff = %v, want now - 15 minutes = %v", cutoff, want)
	}
}

func TestValuationPartiallyPricedSaysNullNotZero(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	goldenTrades(b, p.ID)
	b.trade(p.ID, "NVDA", "buy", "1", "50", "0", time.Hour)
	b.trade(p.ID, "MSFT", "buy", "1", "30", "0", time.Hour)
	h.prices.setPrice("AAPL", "110")

	resp, body := b.getValuation(p.ID)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET valuation = %d %s", resp.StatusCode, body)
	}
	raw := rawObject(t, body)
	if raw["coverage"] != "partial" || raw["market_value"] != nil || raw["unrealized_pnl"] != nil || raw["priced_market_value"] != "165" {
		t.Errorf("valuation = %v, want partial with null totals and a priced subtotal of 165", raw)
	}
	got := decodeValuation(t, body)
	if len(got.MissingSymbols) != 2 || got.MissingSymbols[0] != "MSFT" || got.MissingSymbols[1] != "NVDA" {
		t.Errorf("missing_symbols = %v, want [MSFT NVDA]", got.MissingSymbols)
	}
	for _, pos := range got.Positions {
		switch pos.Symbol {
		case "MSFT", "NVDA":
			if pos.Quote != nil || pos.MarketValue != nil || pos.UnrealizedPnL != nil || pos.UnavailableReason == nil || *pos.UnavailableReason != "no_price" {
				t.Errorf("%s = %+v, want no quote, null values and reason no_price", pos.Symbol, pos)
			}
		}
	}
}

// rawPositions returns the positions of a valuation body by symbol, as raw JSON objects.
func rawPositions(t *testing.T, body []byte) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, p := range rawObject(t, body)["positions"].([]any) {
		position := p.(map[string]any)
		out[position["symbol"].(string)] = position
	}
	return out
}

// requireNull fails unless the object carries the key with the value null: a field that is left out
// is not the same contract as one that is null.
func requireNull(t *testing.T, label string, object map[string]any, key string) {
	t.Helper()
	if value, ok := object[key]; !ok || value != nil {
		t.Errorf("%s.%s = %v (present %t), want null", label, key, value, ok)
	}
}

func TestValuationReportsPercentages(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	goldenTrades(b, p.ID) // 1.5 AAPL left on a cost of 151.5
	b.trade(p.ID, "MSFT", "buy", "1", "55", "0", time.Hour)
	b.trade(p.ID, "NVDA", "buy", "1", "20", "0", 50*time.Minute)
	b.trade(p.ID, "NVDA", "sell", "1", "20", "0", 40*time.Minute) // sold out
	h.prices.setPrice("AAPL", "110")                              // 165, +13.5
	h.prices.setPrice("MSFT", "55")                               // 55, break even

	_, body := b.getValuation(p.ID)
	raw := rawObject(t, body)
	// Cost 151.5 + 55 = 206.5, value 220, unrealized 13.5: 13.5 / 206.5 x 100 = 2700 / 413.
	if raw["cost_basis"] != "206.5" || raw["unrealized_pnl_percent"] != "6.5375302663438256658595641646" {
		t.Errorf("cost_basis = %v, unrealized_pnl_percent = %v", raw["cost_basis"], raw["unrealized_pnl_percent"])
	}
	positions := rawPositions(t, body)
	aapl, msft, nvda := positions["AAPL"], positions["MSFT"], positions["NVDA"]
	if aapl["unrealized_pnl_percent"] != "8.9108910891089108910891089109" || aapl["weight_percent"] != "75" {
		t.Errorf("AAPL percent = %v, weight = %v; want 900/101 and 75", aapl["unrealized_pnl_percent"], aapl["weight_percent"])
	}
	if msft["unrealized_pnl_percent"] != "0" || msft["weight_percent"] != "25" {
		t.Errorf("MSFT percent = %v, weight = %v; want a real 0 (break even) and 25", msft["unrealized_pnl_percent"], msft["weight_percent"])
	}
	requireNull(t, "NVDA (sold out)", nvda, "unrealized_pnl_percent")
	if nvda["weight_percent"] != "0" {
		t.Errorf("NVDA (sold out) weight = %v, want 0", nvda["weight_percent"])
	}
}

func TestValuationPercentagesAreNullWhenAPriceIsMissing(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	b.trade(p.ID, "AAPL", "buy", "1", "100", "0", 2*time.Hour)
	b.trade(p.ID, "MSFT", "buy", "1", "30", "0", time.Hour)
	h.prices.setPrice("AAPL", "110")

	_, body := b.getValuation(p.ID)
	raw := rawObject(t, body)
	if raw["coverage"] != "partial" || raw["cost_basis"] != "130" {
		t.Fatalf("coverage = %v, cost_basis = %v; want partial and a cost basis that needs no price", raw["coverage"], raw["cost_basis"])
	}
	requireNull(t, "valuation", raw, "unrealized_pnl_percent")
	positions := rawPositions(t, body)
	if positions["AAPL"]["unrealized_pnl_percent"] != "10" {
		t.Errorf("AAPL percent = %v, want 10: its own price is known", positions["AAPL"]["unrealized_pnl_percent"])
	}
	requireNull(t, "AAPL", positions["AAPL"], "weight_percent")
	requireNull(t, "MSFT", positions["MSFT"], "unrealized_pnl_percent")
	requireNull(t, "MSFT", positions["MSFT"], "weight_percent")
}

func TestValuationWithoutPricesKeepsRealizedAndSaysUnavailable(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	goldenTrades(b, p.ID)
	h.prices.setDown(true)

	resp, body := b.getValuation(p.ID)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET valuation = %d %s, want 200 even though the provider is down", resp.StatusCode, body)
	}
	raw := rawObject(t, body)
	if raw["coverage"] != "unavailable" || raw["market_value"] != nil || raw["unrealized_pnl"] != nil ||
		raw["priced_market_value"] != "0" || raw["realized_pnl"] != "8.5" {
		t.Errorf("valuation = %v, want unavailable, null totals and realized 8.5", raw)
	}
	pos := decodeValuation(t, body).Positions[0]
	if pos.Quote != nil || pos.UnavailableReason == nil || *pos.UnavailableReason != "provider_unavailable" {
		t.Errorf("position = %+v, want no quote and provider_unavailable", pos)
	}
	if hold, _ := b.getHoldings(p.ID); hold.StatusCode != nethttp.StatusOK {
		t.Errorf("holdings = %d, want 200 while the provider is down", hold.StatusCode)
	}
}

func TestValuationOfAnEmptyPortfolio(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Empty", "")

	resp, body := b.getValuation(p.ID)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET valuation = %d %s", resp.StatusCode, body)
	}
	got := decodeValuation(t, body)
	if got.Coverage != "complete" || got.MarketValue == nil || *got.MarketValue != "0" || got.UnrealizedPnL == nil || *got.UnrealizedPnL != "0" ||
		got.RealizedPnL != "0" || got.PricedMarketValue != "0" || got.Positions == nil || len(got.Positions) != 0 {
		t.Errorf("valuation = %+v", got)
	}
	if raw := rawObject(t, body); raw["cost_basis"] != "0" {
		t.Errorf("cost_basis = %v, want 0", raw["cost_basis"])
	} else {
		requireNull(t, "empty valuation", raw, "unrealized_pnl_percent")
	}
	if !strings.Contains(string(body), `"positions":[]`) || !strings.Contains(string(body), `"missing_symbols":[]`) {
		t.Errorf("body %s should carry empty arrays, not null", body)
	}
	hold, holdBody := b.getHoldings(p.ID)
	if hold.StatusCode != nethttp.StatusOK || !strings.Contains(string(holdBody), `"data":[]`) {
		t.Errorf("holdings = %d %s, want 200 with an empty list", hold.StatusCode, holdBody)
	}
	if h.prices.callCount() != 0 {
		t.Errorf("an empty portfolio asked the provider %d times", h.prices.callCount())
	}
}

func TestHoldingsAndValuationOfAnotherUsersPortfolioLookMissing(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	owner, _ := signedIn(t, h)
	stranger, _ := signedIn(t, h)
	p := owner.mustCreatePortfolio("Mine", "")
	goldenTrades(owner, p.ID)
	h.prices.setPrice("AAPL", "110")
	missing := mustTestID(t).String()

	for name, path := range map[string]func(string) string{"holdings": holdingsPath, "valuation": valuationPath} {
		t.Run(name, func(t *testing.T) {
			foreign, foreignBody := stranger.do(nethttp.MethodGet, path(p.ID), nil, nil)
			unknown, unknownBody := stranger.do(nethttp.MethodGet, path(missing), nil, nil)
			if foreign.StatusCode != nethttp.StatusNotFound || unknown.StatusCode != nethttp.StatusNotFound {
				t.Fatalf("statuses = %d / %d, want 404 / 404", foreign.StatusCode, unknown.StatusCode)
			}
			var a, c ErrorResponse
			mustUnmarshal(t, foreignBody, &a)
			mustUnmarshal(t, unknownBody, &c)
			if a.Error.Code != "not_found" || a.Error.Code != c.Error.Code || a.Error.Message != c.Error.Message {
				t.Errorf("foreign = %+v, unknown = %+v; want the same code and message", a.Error, c.Error)
			}
		})
	}
	if h.prices.callCount() != 0 {
		t.Errorf("the provider was asked %d times for portfolios that are not the caller's", h.prices.callCount())
	}
	for _, e := range h.auditByTarget(t, p.ID) {
		if e.EventType == "security.access_denied" {
			t.Errorf("a read recorded %s; only writes are audited as access denials", e.EventType)
		}
	}
}

func TestHoldingsAndValuationNeedASessionAndAValidID(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	anonymous := h.newBrowser(t)
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Mine", "")

	for _, path := range []string{holdingsPath(p.ID), valuationPath(p.ID)} {
		if resp, body := anonymous.do(nethttp.MethodGet, path, nil, nil); resp.StatusCode != nethttp.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d %s, want 401", path, resp.StatusCode, body)
		}
		if resp, _ := anonymous.do(nethttp.MethodHead, path, nil, nil); resp.StatusCode != nethttp.StatusUnauthorized {
			t.Errorf("anonymous HEAD %s = %d, want 401", path, resp.StatusCode)
		}
	}
	for _, path := range []string{holdingsPath("not-a-uuid"), valuationPath("not-a-uuid")} {
		resp, body := b.do(nethttp.MethodGet, path, nil, nil)
		if resp.StatusCode != nethttp.StatusBadRequest || errorCode(t, body) != "invalid_input" {
			t.Errorf("GET %s = %d %s, want 400 invalid_input", path, resp.StatusCode, body)
		}
	}
}

func TestValuationSeesOnlyThisPortfoliosTrades(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	a := b.mustCreatePortfolio("A", "")
	c := b.mustCreatePortfolio("B", "")
	b.trade(a.ID, "AAPL", "buy", "1", "100", "0", time.Hour)
	b.trade(c.ID, "AAPL", "buy", "5", "100", "0", time.Hour)
	h.prices.setPrice("AAPL", "110")

	for portfolio, want := range map[string]string{a.ID: "110", c.ID: "550"} {
		_, body := b.getValuation(portfolio)
		got := decodeValuation(t, body)
		if got.MarketValue == nil || *got.MarketValue != want {
			t.Errorf("market value of %s = %v, want %s", portfolio, got.MarketValue, want)
		}
	}
}

func mustUnmarshal(t *testing.T, body []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
}

func TestValuationRoutesAreOptional(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	for _, path := range []string{holdingsPath(p.ID), valuationPath(p.ID)} {
		if resp, body := b.do(nethttp.MethodGet, path, nil, nil); resp.StatusCode != nethttp.StatusNotFound {
			t.Errorf("GET %s without the valuation service = %d %s, want 404", path, resp.StatusCode, body)
		}
	}
}

func TestValuationRoutesNeedTheAuthService(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewServer with valuation but no auth service did not panic")
		}
	}()
	log := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	NewServer(Options{Logger: log, Valuation: ValuationOptions{Service: unusedValuationService{}}})
}

type unusedValuationService struct{ port.ValuationService }
