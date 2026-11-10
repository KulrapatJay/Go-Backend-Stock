package http

import (
	nethttp "net/http"
	"strings"
	"testing"
	"time"
)

const accountValuationPath = "/api/v1/valuation"

func (b *browser) getAccountValuation() (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, accountValuationPath, nil, nil)
}

// rawRows returns the objects of a list field of a JSON body, keyed by one of their fields.
func rawRows(t *testing.T, body []byte, list, key string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, item := range rawObject(t, body)[list].([]any) {
		row := item.(map[string]any)
		out[row[key].(string)] = row
	}
	return out
}

func TestAccountValuationAddsUpEveryPortfolioOfTheUser(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	long := b.mustCreatePortfolio("Long term", "")
	short := b.mustCreatePortfolio("Short term", "")
	b.trade(long.ID, "AAPL", "buy", "2", "100", "0", 3*time.Hour)
	b.trade(long.ID, "NVDA", "buy", "1", "10", "0", 3*time.Hour)
	b.trade(long.ID, "NVDA", "sell", "1", "12", "0", 2*time.Hour) // sold out, realized 2
	b.trade(short.ID, "AAPL", "buy", "3", "120", "0", 2*time.Hour)
	b.trade(short.ID, "MSFT", "buy", "1", "50", "0", time.Hour)
	h.prices.setPrice("AAPL", "150")
	h.prices.setPrice("MSFT", "50")

	resp, body := b.getAccountValuation()
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET %s = %d %s", accountValuationPath, resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	// Long: 300 on a cost of 200. Short: 450 + 50 = 500 on 360 + 50 = 410. All: 800 on 610, +190.
	raw := rawObject(t, body)
	want := map[string]any{
		"currency": "USD", "coverage": "complete", "cost_basis": "610", "market_value": "800", "priced_market_value": "800",
		"unrealized_pnl": "190", "unrealized_pnl_percent": "31.1475409836065573770491803279", "realized_pnl": "2",
	}
	for field, value := range want {
		if raw[field] != value {
			t.Errorf("%s = %v, want %v", field, raw[field], value)
		}
	}
	if !strings.Contains(string(body), `"missing_symbols":[]`) {
		t.Errorf("body %s should carry missing_symbols as an empty array", body)
	}
	if at, err := time.Parse(time.RFC3339Nano, raw["calculated_at"].(string)); err != nil || !at.Equal(h.clock.Now()) {
		t.Errorf("calculated_at = %v, want the clock %v", raw["calculated_at"], h.clock.Now())
	}

	portfolios := rawRows(t, body, "portfolios", "portfolio_id")
	if len(portfolios) != 2 {
		t.Fatalf("portfolios = %v, want both", portfolios)
	}
	for field, value := range map[string]any{
		"name": "Long term", "coverage": "complete", "cost_basis": "200", "market_value": "300", "unrealized_pnl": "100",
		"unrealized_pnl_percent": "50", "realized_pnl": "2", "weight_percent": "37.5",
	} {
		if portfolios[long.ID][field] != value {
			t.Errorf("Long term %s = %v, want %v", field, portfolios[long.ID][field], value)
		}
	}
	if portfolios[short.ID]["market_value"] != "500" || portfolios[short.ID]["weight_percent"] != "62.5" || portfolios[short.ID]["name"] != "Short term" {
		t.Errorf("Short term = %v, want 500 and 62.5%%", portfolios[short.ID])
	}
	// The order of GET /api/v1/portfolios: newest first.
	if first := rawObject(t, body)["portfolios"].([]any)[0].(map[string]any); first["portfolio_id"] != short.ID {
		t.Errorf("first portfolio = %v, want the newest (Short term)", first["portfolio_id"])
	}

	allocation := rawObject(t, body)["allocation"].([]any)
	if len(allocation) != 2 {
		t.Fatalf("allocation = %v, want AAPL and MSFT (the sold-out NVDA is not held)", allocation)
	}
	aapl, msft := allocation[0].(map[string]any), allocation[1].(map[string]any)
	if aapl["symbol"] != "AAPL" || aapl["quantity"] != "5" || aapl["market_value"] != "750" || aapl["weight_percent"] != "93.75" ||
		aapl["instrument_name"] == "" || aapl["unavailable_reason"] != nil {
		t.Errorf("AAPL allocation = %v, want both portfolios merged: 5 shares, 750, 93.75%%", aapl)
	}
	if quote, ok := aapl["quote"].(map[string]any); !ok || quote["price"] != "150" || quote["is_demo"] != true {
		t.Errorf("AAPL quote = %v, want the price the valuation used", aapl["quote"])
	}
	if msft["symbol"] != "MSFT" || msft["quantity"] != "1" || msft["market_value"] != "50" || msft["weight_percent"] != "6.25" {
		t.Errorf("MSFT allocation = %v", msft)
	}

	if h.prices.callCount() != 1 {
		t.Fatalf("the provider was asked %d times, want once for the whole account", h.prices.callCount())
	}
	h.prices.mu.Lock()
	asked := strings.Join(h.prices.asked[0], ",")
	h.prices.mu.Unlock()
	if asked != "AAPL,MSFT" {
		t.Errorf("symbols asked = %s, want AAPL once although two portfolios hold it", asked)
	}

	head, headBody := b.do(nethttp.MethodHead, accountValuationPath, nil, nil)
	if head.StatusCode != nethttp.StatusOK || len(headBody) != 0 {
		t.Errorf("HEAD = %d with %d body bytes, want 200 and none", head.StatusCode, len(headBody))
	}
}

func TestAccountValuationWithAMissingPriceSaysNullNotZero(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	priced := b.mustCreatePortfolio("Priced", "")
	mixed := b.mustCreatePortfolio("Mixed", "")
	b.trade(priced.ID, "AAPL", "buy", "1", "100", "0", 2*time.Hour)
	b.trade(mixed.ID, "AAPL", "buy", "1", "100", "0", 2*time.Hour)
	b.trade(mixed.ID, "MSFT", "buy", "1", "30", "0", time.Hour)
	h.prices.setPrice("AAPL", "110")

	resp, body := b.getAccountValuation()
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET = %d %s", resp.StatusCode, body)
	}
	raw := rawObject(t, body)
	if raw["coverage"] != "partial" || raw["priced_market_value"] != "220" || raw["cost_basis"] != "230" {
		t.Errorf("account = %v, want partial with a priced subtotal of 220 and a cost of 230", raw)
	}
	for _, field := range []string{"market_value", "unrealized_pnl", "unrealized_pnl_percent"} {
		requireNull(t, "account", raw, field)
	}
	if missing := raw["missing_symbols"].([]any); len(missing) != 1 || missing[0] != "MSFT" {
		t.Errorf("missing_symbols = %v, want [MSFT]", missing)
	}
	portfolios := rawRows(t, body, "portfolios", "portfolio_id")
	if portfolios[priced.ID]["coverage"] != "complete" || portfolios[priced.ID]["market_value"] != "110" || portfolios[priced.ID]["unrealized_pnl_percent"] != "10" {
		t.Errorf("the priced portfolio = %v, want its own complete valuation", portfolios[priced.ID])
	}
	if portfolios[mixed.ID]["coverage"] != "partial" {
		t.Errorf("the mixed portfolio = %v, want partial", portfolios[mixed.ID])
	}
	requireNull(t, "mixed portfolio", portfolios[mixed.ID], "market_value")
	requireNull(t, "priced portfolio", portfolios[priced.ID], "weight_percent") // no share of an unknown total
	allocation := rawRows(t, body, "allocation", "symbol")
	if allocation["AAPL"]["market_value"] != "220" || allocation["AAPL"]["quantity"] != "2" {
		t.Errorf("AAPL allocation = %v, want 2 shares worth 220", allocation["AAPL"])
	}
	requireNull(t, "AAPL allocation", allocation["AAPL"], "weight_percent")
	requireNull(t, "MSFT allocation", allocation["MSFT"], "market_value")
	requireNull(t, "MSFT allocation", allocation["MSFT"], "quote")
	if allocation["MSFT"]["unavailable_reason"] != "no_price" {
		t.Errorf("MSFT allocation = %v, want reason no_price", allocation["MSFT"])
	}
}

func TestAccountValuationWhenTheProviderIsDownStillAnswers(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	goldenTrades(b, p.ID)
	h.prices.setDown(true)

	resp, body := b.getAccountValuation()
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET = %d %s, want 200 even though the provider is down", resp.StatusCode, body)
	}
	raw := rawObject(t, body)
	if raw["coverage"] != "unavailable" || raw["realized_pnl"] != "8.5" || raw["cost_basis"] != "151.5" || raw["priced_market_value"] != "0" {
		t.Errorf("account = %v, want unavailable with realized 8.5 and cost 151.5", raw)
	}
	requireNull(t, "account", raw, "market_value")
	if reason := rawRows(t, body, "allocation", "symbol")["AAPL"]["unavailable_reason"]; reason != "provider_unavailable" {
		t.Errorf("AAPL reason = %v, want provider_unavailable", reason)
	}
}

func TestAccountValuationNeverCountsAnotherUsersPortfolios(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	owner, _ := signedIn(t, h)
	stranger, _ := signedIn(t, h)
	theirs := owner.mustCreatePortfolio("Theirs", "")
	owner.trade(theirs.ID, "AAPL", "buy", "100", "100", "0", time.Hour)
	mine := stranger.mustCreatePortfolio("Mine", "")
	stranger.trade(mine.ID, "AAPL", "buy", "1", "100", "0", time.Hour)
	h.prices.setPrice("AAPL", "110")

	_, body := stranger.getAccountValuation()
	raw := rawObject(t, body)
	if raw["market_value"] != "110" || raw["cost_basis"] != "100" {
		t.Errorf("account = %v, want only the caller's one share", raw)
	}
	portfolios := rawRows(t, body, "portfolios", "portfolio_id")
	if _, leaked := portfolios[theirs.ID]; leaked || len(portfolios) != 1 {
		t.Errorf("portfolios = %v, want only the caller's", portfolios)
	}
	if strings.Contains(string(body), theirs.ID) || strings.Contains(string(body), "Theirs") {
		t.Errorf("body %s mentions another user's portfolio", body)
	}
	if got := rawRows(t, body, "allocation", "symbol")["AAPL"]["quantity"]; got != "1" {
		t.Errorf("AAPL quantity = %v, want 1: another user's 100 shares are not the caller's", got)
	}
}

func TestAccountValuationOfANewUserIsZerosAndEmptyLists(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	b, _ := signedIn(t, h)

	// In this order: the second case adds a portfolio to the same user.
	for _, tc := range []struct {
		name    string
		prepare func()
	}{
		{"no portfolio", func() {}},
		{"an empty portfolio", func() { b.mustCreatePortfolio("Empty", "") }},
	} {
		name := tc.name
		t.Run(name, func(t *testing.T) {
			tc.prepare()
			resp, body := b.getAccountValuation()
			if resp.StatusCode != nethttp.StatusOK {
				t.Fatalf("GET = %d %s, want 200", resp.StatusCode, body)
			}
			raw := rawObject(t, body)
			for field, value := range map[string]any{
				"coverage": "complete", "cost_basis": "0", "market_value": "0", "priced_market_value": "0", "unrealized_pnl": "0", "realized_pnl": "0",
			} {
				if raw[field] != value {
					t.Errorf("%s = %v, want %v", field, raw[field], value)
				}
			}
			requireNull(t, "account", raw, "unrealized_pnl_percent")
			if !strings.Contains(string(body), `"allocation":[]`) || !strings.Contains(string(body), `"missing_symbols":[]`) {
				t.Errorf("body %s should carry empty arrays, not null", body)
			}
			if name == "no portfolio" && !strings.Contains(string(body), `"portfolios":[]`) {
				t.Errorf("body %s should carry portfolios as an empty array", body)
			}
		})
	}
	if h.prices.callCount() != 0 {
		t.Errorf("an account that holds nothing asked the provider %d times", h.prices.callCount())
	}
}

func TestAccountValuationNeedsASession(t *testing.T) {
	h := newAuthHarness(t, withValuation())
	anonymous := h.newBrowser(t)
	if resp, body := anonymous.do(nethttp.MethodGet, accountValuationPath, nil, nil); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("anonymous GET = %d %s, want 401", resp.StatusCode, body)
	}
	if resp, _ := anonymous.do(nethttp.MethodHead, accountValuationPath, nil, nil); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("anonymous HEAD = %d, want 401", resp.StatusCode)
	}
}

func TestAccountValuationRouteIsOptional(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	if resp, body := b.do(nethttp.MethodGet, accountValuationPath, nil, nil); resp.StatusCode != nethttp.StatusNotFound {
		t.Errorf("GET without the valuation service = %d %s, want 404", resp.StatusCode, body)
	}
}
