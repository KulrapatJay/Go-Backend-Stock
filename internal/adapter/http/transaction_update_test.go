package http

import (
	"context"
	nethttp "net/http"
	"strings"
	"testing"
	"time"
)

// updateTrade asks for a token first. ifMatch "" sends no If-Match header. PUT replaces the whole
// trade, so the body is the caller's to build.
func (b *browser) updateTrade(portfolioID, id, ifMatch string, body any) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPut, transactionsPath(portfolioID)+"/"+id, body, ifMatchHeader(ifMatch))
}

func (b *browser) deleteTrade(portfolioID, id, ifMatch string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodDelete, transactionsPath(portfolioID)+"/"+id, nil, ifMatchHeader(ifMatch))
}

// tradeState reads a transaction with admin rights, so a test sees what the API left behind.
type tradeState struct {
	Found        bool
	PortfolioID  string
	InstrumentID string
	Symbol       string
	Version      int
	Sequence     string
	Quantity     string
	Price        string
}

func (h *authHarness) tradeState(t *testing.T, id string) tradeState {
	t.Helper()
	var s tradeState
	rows, err := h.admin.Query(context.Background(),
		`SELECT t.portfolio_id::text, t.instrument_id::text, i.symbol, t.version, t.sequence::text, t.quantity::text, t.price::text
		   FROM app.transactions t JOIN app.instruments i ON i.id = t.instrument_id WHERE t.id = $1`, id)
	if err != nil {
		t.Fatalf("read transaction: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		s.Found = true
		if err := rows.Scan(&s.PortfolioID, &s.InstrumentID, &s.Symbol, &s.Version, &s.Sequence, &s.Quantity, &s.Price); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return s
}

func (h *authHarness) instrumentID(t *testing.T, symbol string) string {
	t.Helper()
	var id string
	if err := h.admin.QueryRow(context.Background(), `SELECT id::text FROM app.instruments WHERE symbol = $1`, symbol).Scan(&id); err != nil {
		t.Fatalf("instrument %s: %v", symbol, err)
	}
	return id
}

// replacement is a full valid PUT body for the symbol with another price.
func (h *authHarness) replacement(symbol, price string) map[string]string {
	req := h.tradeRequest(symbol)
	req["price"] = price
	return req
}

func TestUpdateTrade(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, user := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	created := b.mustCreateTrade(p.ID, h.tradeRequest(symbol))
	h.clock.Advance(time.Minute)

	resp, body := b.updateTrade(p.ID, created.ID, `"1"`, h.replacement(symbol, "210.5"))
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, body)
	}
	got := decodeTransaction(t, body)
	if got.Version != 2 || got.Price != "210.5" || got.Symbol != symbol || got.ID != created.ID || got.PortfolioID != p.ID ||
		got.Sequence != created.Sequence || !got.CreatedAt.Equal(created.CreatedAt) || !got.UpdatedAt.After(created.UpdatedAt) ||
		!got.UpdatedAt.Equal(h.clock.Now()) {
		t.Errorf("updated trade = %+v, created %+v", got, created)
	}
	if v := resp.Header.Get("ETag"); v != `"2"` {
		t.Errorf("ETag = %q, want \"2\"", v)
	}
	if v := resp.Header.Get("Cache-Control"); v != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", v)
	}
	if again, data := b.getTrade(p.ID, created.ID); again.StatusCode != nethttp.StatusOK || decodeTransaction(t, data) != got || again.Header.Get("ETag") != `"2"` {
		t.Errorf("get after update = %d %s, want the updated trade at ETag \"2\"", again.StatusCode, data)
	}
	if state := h.portfolioState(t, p.ID); state.Version != 1 {
		t.Errorf("portfolio version = %d, want 1 (a trade change does not touch the portfolio)", state.Version)
	}

	events := h.auditByTarget(t, created.ID)
	if len(events) != 2 || events[1].EventType != "transaction.updated" || events[1].Actor != user.ID || events[1].Outcome != "success" {
		t.Fatalf("audit = %+v, want transaction.created then transaction.updated by the caller", events)
	}
	before, _ := events[1].Metadata["before"].(map[string]any)
	after, _ := events[1].Metadata["after"].(map[string]any)
	if before["price"] != "200" || before["symbol"] != symbol || after["price"] != "210.5" || after["symbol"] != symbol || after["quantity"] != "0.5" {
		t.Errorf("audit before/after = %v / %v, want price 200 then 210.5", before, after)
	}
	if events[1].Metadata["portfolio_id"] != p.ID {
		t.Errorf("audit portfolio_id = %v, want %s", events[1].Metadata["portfolio_id"], p.ID)
	}
}

func TestUpdateTradeToAnotherSymbol(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	oldSymbol, newSymbol := h.freshSymbol(t), h.freshSymbol(t)
	created := b.mustCreateTrade(p.ID, h.tradeRequest(oldSymbol))

	resp, body := b.updateTrade(p.ID, created.ID, `"1"`, h.tradeRequest(newSymbol))
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, body)
	}
	if got := decodeTransaction(t, body); got.Symbol != newSymbol {
		t.Errorf("symbol = %q, want %q", got.Symbol, newSymbol)
	}
	state := h.tradeState(t, created.ID)
	if state.Symbol != newSymbol || state.InstrumentID != h.instrumentID(t, newSymbol) || state.InstrumentID == h.instrumentID(t, oldSymbol) {
		t.Errorf("stored trade = %+v, want the instrument of %s", state, newSymbol)
	}
	if n := h.instruments.lookups(newSymbol); n != 1 {
		t.Errorf("provider lookups of %s = %d, want 1", newSymbol, n)
	}

	// A symbol the provider does not know changes nothing.
	unknown := "UNK" + h.freshSymbol(t)[1:8]
	resp, body = b.updateTrade(p.ID, created.ID, `"2"`, h.tradeRequest(unknown))
	if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "unknown_symbol" {
		t.Errorf("update to an unknown symbol = %d %s, want 422 unknown_symbol", resp.StatusCode, body)
	}
	if after := h.tradeState(t, created.ID); after != state {
		t.Errorf("trade after the refused update = %+v, want %+v", after, state)
	}
}

// PUT replaces the whole trade: a body that leaves a field out would silently keep or erase it, so
// it is refused and nothing is written.
func TestUpdateTradeRequiresEveryField(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	created := b.mustCreateTrade(p.ID, h.tradeRequest(symbol))
	state := h.tradeState(t, created.ID)
	eventsBefore := len(h.auditByTarget(t, created.ID))

	for _, field := range []string{"symbol", "side", "quantity", "price", "fee", "executed_at"} {
		t.Run("without "+field, func(t *testing.T) {
			req := h.replacement(symbol, "300")
			delete(req, field)
			resp, body := b.updateTrade(p.ID, created.ID, `"1"`, req)
			if resp.StatusCode != nethttp.StatusBadRequest || errorCode(t, body) != "invalid_input" {
				t.Errorf("PUT without %s = %d %s, want 400 invalid_input", field, resp.StatusCode, body)
			}
		})
	}
	for name, body := range map[string]any{
		"not JSON":      "{not json",
		"a JSON number": map[string]any{"symbol": symbol, "side": "buy", "quantity": 0.5, "price": "300", "fee": "1", "executed_at": h.clock.Now().Add(-time.Hour).UTC().Format(time.RFC3339)},
		"a future time": map[string]string{"symbol": symbol, "side": "buy", "quantity": "1", "price": "300", "fee": "1", "executed_at": h.clock.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
	} {
		resp, data := b.updateTrade(p.ID, created.ID, `"1"`, body)
		if resp.StatusCode != nethttp.StatusBadRequest {
			t.Errorf("PUT with %s = %d %s, want 400", name, resp.StatusCode, data)
		}
	}
	if after := h.tradeState(t, created.ID); after != state {
		t.Errorf("trade after the refused updates = %+v, want %+v", after, state)
	}
	if n := len(h.auditByTarget(t, created.ID)); n != eventsBefore {
		t.Errorf("audit events = %d, want %d", n, eventsBefore)
	}
	if n := h.instruments.lookups(symbol); n != 1 {
		t.Errorf("provider lookups of %s = %d, want 1 (from the create only)", symbol, n)
	}
}

func TestUpdateAndDeleteTradePreconditions(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	created := b.mustCreateTrade(p.ID, h.tradeRequest(symbol))
	if resp, body := b.updateTrade(p.ID, created.ID, `"1"`, h.replacement(symbol, "201")); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("update to version 2 = %d %s", resp.StatusCode, body)
	}
	state := h.tradeState(t, created.ID)
	eventsBefore := len(h.auditByTarget(t, created.ID))

	cases := []struct {
		name, ifMatch string
		status        int
		code          string
	}{
		{"no If-Match", "", nethttp.StatusPreconditionRequired, "precondition_required"},
		{"stale version", `"1"`, nethttp.StatusPreconditionFailed, "version_conflict"},
		{"wildcard", `*`, nethttp.StatusBadRequest, "invalid_input"},
		{"weak validator", `W/"1"`, nethttp.StatusBadRequest, "invalid_input"},
		{"leading zero", `"03"`, nethttp.StatusBadRequest, "invalid_input"},
	}
	for _, tc := range cases {
		for _, method := range []string{nethttp.MethodPut, nethttp.MethodDelete} {
			t.Run(tc.name+" "+method, func(t *testing.T) {
				var resp response
				var body []byte
				if method == nethttp.MethodPut {
					resp, body = b.updateTrade(p.ID, created.ID, tc.ifMatch, h.replacement(symbol, "999"))
				} else {
					resp, body = b.deleteTrade(p.ID, created.ID, tc.ifMatch)
				}
				if resp.StatusCode != tc.status || errorCode(t, body) != tc.code {
					t.Errorf("%s = %d %s, want %d %s", method, resp.StatusCode, body, tc.status, tc.code)
				}
				if after := h.tradeState(t, created.ID); after != state {
					t.Errorf("trade after the refused %s = %+v, want %+v", method, after, state)
				}
				if n := len(h.auditByTarget(t, created.ID)); n != eventsBefore {
					t.Errorf("audit events = %d, want %d (a refused %s writes none)", n, eventsBefore, method)
				}
			})
		}
	}
}

func TestDeleteTrade(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, user := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	created := b.mustCreateTrade(p.ID, h.tradeRequest(symbol))

	resp, body := b.deleteTrade(p.ID, created.ID, `"1"`)
	if resp.StatusCode != nethttp.StatusNoContent || len(body) != 0 {
		t.Fatalf("delete = %d %s, want 204 with no body", resp.StatusCode, body)
	}
	if resp, body := b.getTrade(p.ID, created.ID); resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("get after delete = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	if n := h.countRows(t, "transactions", p.ID); n != 0 {
		t.Errorf("transactions = %d, want 0", n)
	}
	events := h.auditByTarget(t, created.ID)
	if len(events) != 2 || events[1].EventType != "transaction.deleted" || events[1].Actor != user.ID || events[1].TargetID != created.ID || events[1].Outcome != "success" {
		t.Fatalf("audit = %+v, want transaction.created then transaction.deleted for the same ID", events)
	}
	before, _ := events[1].Metadata["before"].(map[string]any)
	if before["symbol"] != symbol || before["quantity"] != "0.5" || events[1].Metadata["after"] != nil {
		t.Errorf("audit metadata = %v, want the deleted trade as before and no after", events[1].Metadata)
	}
	if resp, body := b.deleteTrade(p.ID, created.ID, `"1"`); resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("second delete = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	if resp, body := b.updateTrade(p.ID, created.ID, `"1"`, h.tradeRequest(symbol)); resp.StatusCode != nethttp.StatusNotFound {
		t.Errorf("update after delete = %d %s, want 404", resp.StatusCode, body)
	}
	if n := len(h.auditByTarget(t, created.ID)); n != 2 {
		t.Errorf("audit events after the repeats = %d, want 2", n)
	}
}

// A change to the past that would leave a position below zero is refused as a whole.
func TestUpdateAndDeleteTradeThatBreakTheHistory(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	buyReq := h.tradeRequest(symbol)
	buyReq["quantity"], buyReq["executed_at"] = "1", h.clock.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339)
	buy := b.mustCreateTrade(p.ID, buyReq)
	sellReq := h.tradeRequest(symbol)
	sellReq["side"], sellReq["quantity"] = "sell", "1"
	b.mustCreateTrade(p.ID, sellReq)
	state := h.tradeState(t, buy.ID)
	eventsBefore := len(h.auditByTarget(t, buy.ID))

	smaller := h.tradeRequest(symbol)
	smaller["quantity"], smaller["executed_at"] = "0.5", buyReq["executed_at"]
	resp, body := b.updateTrade(p.ID, buy.ID, `"1"`, smaller)
	if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "invalid_history" {
		t.Errorf("shrink the buy = %d %s, want 422 invalid_history", resp.StatusCode, body)
	}
	resp, body = b.deleteTrade(p.ID, buy.ID, `"1"`)
	if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "invalid_history" {
		t.Errorf("delete the buy = %d %s, want 422 invalid_history", resp.StatusCode, body)
	}
	if after := h.tradeState(t, buy.ID); after != state {
		t.Errorf("buy after the refused changes = %+v, want %+v", after, state)
	}
	if n := len(h.auditByTarget(t, buy.ID)); n != eventsBefore {
		t.Errorf("audit events = %d, want %d", n, eventsBefore)
	}
}

// TestUpdateAndDeleteTradeNeedCSRFAndOrigin: PUT and DELETE change data, so they are held to the
// same session, CSRF and Origin checks as POST, and a refused call changes nothing.
func TestUpdateAndDeleteTradeNeedCSRFAndOrigin(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	created := b.mustCreateTrade(p.ID, h.tradeRequest(symbol))
	state := h.tradeState(t, created.ID)
	path := transactionsPath(p.ID) + "/" + created.ID

	anonymous := h.newBrowser(t)
	if resp, body := anonymous.updateTrade(p.ID, created.ID, `"1"`, h.replacement(symbol, "1")); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("PUT without a session = %d %s, want 401", resp.StatusCode, body)
	}
	if resp, body := anonymous.deleteTrade(p.ID, created.ID, `"1"`); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("DELETE without a session = %d %s, want 401", resp.StatusCode, body)
	}

	b.fetchCSRF()
	good := b.csrf
	// A genuine token issued to somebody else's session: well formed and correctly signed, refused
	// only because it is bound to the other session.
	other, _ := signedIn(t, h)
	other.fetchCSRF()
	cases := []struct {
		name    string
		csrf    string
		headers map[string]string
	}{
		{"missing token", "", nil},
		{"token bound to another session", other.csrf, nil},
		{"foreign origin", good, map[string]string{"Origin": "http://evil.example"}},
	}
	for _, tc := range cases {
		for _, method := range []string{nethttp.MethodPut, nethttp.MethodDelete} {
			t.Run(tc.name+" "+method, func(t *testing.T) {
				b.csrf = tc.csrf
				headers := map[string]string{"If-Match": `"1"`}
				for k, v := range tc.headers {
					headers[k] = v
				}
				var body any
				if method == nethttp.MethodPut {
					body = h.replacement(symbol, "1")
				}
				resp, data := b.do(method, path, body, headers)
				if resp.StatusCode != nethttp.StatusForbidden || errorCode(t, data) != "csrf_failed" {
					t.Errorf("%s = %d %s, want 403 csrf_failed", method, resp.StatusCode, data)
				}
				if after := h.tradeState(t, created.ID); after != state {
					t.Errorf("trade after the refused %s = %+v, want %+v", method, after, state)
				}
			})
		}
	}
}

// A trade belongs to one portfolio. Reaching it through another portfolio of the same owner gets
// the 404 of a trade that does not exist, and a refused PUT does not move it.
func TestATradeCannotBeReachedThroughAnotherPortfolio(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	home := b.mustCreatePortfolio("Long term", "")
	away := b.mustCreatePortfolio("Short term", "")
	symbol := h.freshSymbol(t)
	created := b.mustCreateTrade(home.ID, h.tradeRequest(symbol))
	state := h.tradeState(t, created.ID)
	eventsBefore := len(h.auditByTarget(t, created.ID))

	resp, body := b.getTrade(away.ID, created.ID)
	if resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("GET through the other portfolio = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	resp, body = b.updateTrade(away.ID, created.ID, `"1"`, h.replacement(symbol, "300"))
	if resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("PUT through the other portfolio = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	resp, body = b.deleteTrade(away.ID, created.ID, `"1"`)
	if resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("DELETE through the other portfolio = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	if after := h.tradeState(t, created.ID); after != state || after.PortfolioID != home.ID {
		t.Errorf("trade after the refused calls = %+v, want %+v in %s", after, state, home.ID)
	}
	if h.countRows(t, "transactions", home.ID) != 1 || h.countRows(t, "transactions", away.ID) != 0 {
		t.Errorf("transactions: home %d, away %d, want 1 and 0", h.countRows(t, "transactions", home.ID), h.countRows(t, "transactions", away.ID))
	}
	if n := len(h.auditByTarget(t, created.ID)); n != eventsBefore {
		t.Errorf("audit events = %d, want %d (your own portfolios are not an access denial)", n, eventsBefore)
	}
}

// Another user's trade looks as missing as an unknown one, for PUT and DELETE too.
func TestUpdateAndDeleteTradeOfAnotherUserLookMissing(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	owner, _ := signedIn(t, h)
	p := owner.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	created := owner.mustCreateTrade(p.ID, h.tradeRequest(symbol))
	state := h.tradeState(t, created.ID)

	stranger, _ := signedIn(t, h)
	for _, id := range []string{created.ID, mustTestID(t).String()} {
		resp, body := stranger.updateTrade(p.ID, id, `"1"`, h.replacement(symbol, "1"))
		if resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
			t.Errorf("stranger PUT %s = %d %s, want 404 not_found", id, resp.StatusCode, body)
		}
		resp, body = stranger.deleteTrade(p.ID, id, `"1"`)
		if resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
			t.Errorf("stranger DELETE %s = %d %s, want 404 not_found", id, resp.StatusCode, body)
		}
	}
	if after := h.tradeState(t, created.ID); after != state {
		t.Errorf("trade after the stranger's calls = %+v, want %+v", after, state)
	}
	if resp, body := owner.updateTrade(p.ID, "abc", `"1"`, h.replacement(symbol, "1")); resp.StatusCode != nethttp.StatusBadRequest || !strings.Contains(string(body), "transaction_id") {
		t.Errorf("PUT with a bad transaction_id = %d %s, want 400 naming transaction_id", resp.StatusCode, body)
	}
}
