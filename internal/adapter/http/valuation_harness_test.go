package http

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// withValuation serves the holdings and valuation routes with trades and portfolios, over a fake
// price provider the test controls.
func withValuation() harnessOption {
	return func(c *harnessConfig) { c.portfolios, c.trades, c.valuation = true, true, true }
}

// fakePriceProvider prices the symbols a test gave it, records how it was asked, and can be switched
// off to play a provider that is down.
type fakePriceProvider struct {
	mu      sync.Mutex
	prices  map[string]string // symbol -> price
	down    bool
	calls   int
	asked   [][]string
	cutoffs []time.Time
}

func (f *fakePriceProvider) setPrice(symbol, price string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prices[symbol] = price
}

func (f *fakePriceProvider) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

func (f *fakePriceProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakePriceProvider) Quotes(_ context.Context, symbols []domain.Symbol, cutoff time.Time) (domain.QuoteBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	names := make([]string, len(symbols))
	for i, s := range symbols {
		names[i] = s.String()
	}
	f.asked, f.cutoffs = append(f.asked, names), append(f.cutoffs, cutoff)
	if f.down {
		return domain.QuoteBatch{}, errors.New("price provider is down")
	}
	batch := domain.QuoteBatch{Quotes: map[string]domain.PriceQuote{}}
	for _, symbol := range symbols {
		raw, ok := f.prices[symbol.String()]
		if !ok {
			batch.Unavailable = append(batch.Unavailable, symbol)
			continue
		}
		price, err := domain.ParsePrice(raw)
		if err != nil {
			return domain.QuoteBatch{}, err
		}
		asOf := cutoff.UTC().Truncate(time.Minute)
		quote, err := domain.NewPriceQuote(domain.PriceQuote{
			Symbol: symbol, Price: price, Currency: "USD", AsOf: asOf, FetchedAt: asOf.Add(time.Minute),
			Source: "test", Feed: "test", IsDemo: true, SessionStatus: domain.SessionUnknown,
		})
		if err != nil {
			return domain.QuoteBatch{}, err
		}
		batch.Quotes[symbol.String()] = quote
	}
	return batch, nil
}

func holdingsPath(portfolioID string) string {
	return "/api/v1/portfolios/" + portfolioID + "/holdings"
}

func valuationPath(portfolioID string) string {
	return "/api/v1/portfolios/" + portfolioID + "/valuation"
}

// trade records one trade with a fresh key, executed ago before the harness clock.
func (b *browser) trade(portfolioID, symbol, side, quantity, price, fee string, ago time.Duration) {
	b.t.Helper()
	body := map[string]string{
		"symbol": symbol, "side": side, "quantity": quantity, "price": price, "fee": fee,
		"executed_at": b.h.clock.Now().Add(-ago).UTC().Format(time.RFC3339),
	}
	b.mustCreateTrade(portfolioID, body)
}

func (b *browser) getHoldings(portfolioID string) (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, holdingsPath(portfolioID), nil, nil)
}

func (b *browser) getValuation(portfolioID string) (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, valuationPath(portfolioID), nil, nil)
}

func decodeHoldings(t *testing.T, body []byte) HoldingsResponse {
	t.Helper()
	var out HoldingsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("holdings body %s: %v", body, err)
	}
	return out
}

func decodeValuation(t *testing.T, body []byte) ValuationResponse {
	t.Helper()
	var out ValuationResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("valuation body %s: %v", body, err)
	}
	return out
}

// rawObject decodes a JSON object so a test can tell a field that is null from one that is "0".
func rawObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	return out
}
