package http

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// withTransactions serves the transaction routes over the real service and database, with a fake
// instrument provider. It turns the portfolio routes on too: a trade needs a portfolio.
func withTransactions() harnessOption {
	return func(c *harnessConfig) { c.portfolios, c.trades = true, true }
}

// fakeInstrumentProvider knows every symbol except those starting with "UNK", counts the lookups
// per symbol, and can be switched off to play a provider that is down.
type fakeInstrumentProvider struct {
	mu    sync.Mutex
	down  bool
	calls map[string]int
	total int
}

func (f *fakeInstrumentProvider) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

// lookups is how many times the provider was asked about symbol.
func (f *fakeInstrumentProvider) lookups(symbol string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[symbol]
}

func (f *fakeInstrumentProvider) totalLookups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total
}

func (f *fakeInstrumentProvider) Lookup(_ context.Context, symbol domain.Symbol) (domain.Instrument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[symbol.String()]++
	f.total++
	switch {
	case f.down:
		return domain.Instrument{}, errors.New("instrument provider is down")
	case strings.HasPrefix(symbol.String(), "UNK"):
		return domain.Instrument{}, domain.ErrUnknownSymbol
	}
	return domain.Instrument{
		Symbol: symbol, Name: "Test Corp", Exchange: "NASDAQ", AssetClass: domain.AssetClassUSEquity,
		Status: domain.InstrumentStatusActive, Source: domain.InstrumentSourceDemo,
	}, nil
}

// tradeRequest is a valid body: a buy of 0.5 share at 200 with a fee of 1, an hour before the
// harness clock.
func (h *authHarness) tradeRequest(symbol string) map[string]string {
	return map[string]string{
		"symbol": symbol, "side": "buy", "quantity": "0.5", "price": "200", "fee": "1",
		"executed_at": h.clock.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	}
}

func transactionsPath(portfolioID string) string {
	return "/api/v1/portfolios/" + portfolioID + "/transactions"
}

// createTrade asks for a CSRF token first, as a page would. key "" sends no Idempotency-Key.
func (b *browser) createTrade(portfolioID, key string, body any) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, transactionsPath(portfolioID), body, map[string]string{"Idempotency-Key": key})
}

// newKey returns an Idempotency-Key no other call uses.
func newKey(t *testing.T) string {
	t.Helper()
	return "key-" + mustTestID(t).String()
}

// mustCreateTrade records a trade with a fresh key and fails the test unless the API answers 201.
func (b *browser) mustCreateTrade(portfolioID string, body map[string]string) TransactionResponse {
	b.t.Helper()
	resp, data := b.createTrade(portfolioID, newKey(b.t), body)
	if resp.StatusCode != nethttp.StatusCreated {
		b.t.Fatalf("create trade %v = %d %s", body, resp.StatusCode, data)
	}
	return decodeTransaction(b.t, data)
}

func (b *browser) getTrade(portfolioID, id string) (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, transactionsPath(portfolioID)+"/"+id, nil, nil)
}

// listTrades lists with a raw query such as "limit=5&symbol=AAPL"; "" lists the first page.
func (b *browser) listTrades(portfolioID, query string) (response, []byte) {
	b.t.Helper()
	path := transactionsPath(portfolioID)
	if query != "" {
		path += "?" + query
	}
	return b.do(nethttp.MethodGet, path, nil, nil)
}

func decodeTransaction(t *testing.T, body []byte) TransactionResponse {
	t.Helper()
	var tr TransactionResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		t.Fatalf("transaction body %s: %v", body, err)
	}
	return tr
}

func decodeTransactionPage(t *testing.T, body []byte) TransactionPageResponse {
	t.Helper()
	var page TransactionPageResponse
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("transaction page body %s: %v", body, err)
	}
	return page
}

func (h *authHarness) instrumentExists(t *testing.T, symbol string) bool {
	t.Helper()
	var n int
	if err := h.admin.QueryRow(context.Background(), `SELECT count(*) FROM app.instruments WHERE symbol = $1`, symbol).Scan(&n); err != nil {
		t.Fatalf("count instruments: %v", err)
	}
	return n > 0
}
