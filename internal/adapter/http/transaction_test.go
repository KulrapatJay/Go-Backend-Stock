package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	nethttp "net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/port"
)

func TestCreateTradeReturnsItsRepresentation(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, user := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	req := h.tradeRequest(symbol)
	req["quantity"] = "0.50"
	req["fee"] = "1.00"

	resp, body := b.createTrade(p.ID, newKey(t), req)
	if resp.StatusCode != nethttp.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	got := decodeTransaction(t, body)
	wantAt, err := time.Parse(time.RFC3339, req["executed_at"])
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.PortfolioID != p.ID || got.Symbol != symbol || got.Side != "buy" ||
		got.Quantity != "0.5" || got.Price != "200" || got.Fee != "1" || !got.ExecutedAt.Equal(wantAt) ||
		got.Version != 1 || !got.CreatedAt.Equal(h.clock.Now()) || !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("transaction = %+v", got)
	}
	if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(got.Sequence) {
		t.Errorf("sequence = %q, want a decimal integer string", got.Sequence)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"id", "portfolio_id", "sequence", "symbol", "side", "quantity", "price", "fee", "executed_at", "version", "created_at", "updated_at"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("body has no %q: %s", name, body)
		}
	}
	if len(fields) != 12 {
		t.Errorf("body has %d fields, want 12: %s", len(fields), body)
	}
	for _, name := range []string{"sequence", "quantity", "price", "fee"} {
		if !bytes.HasPrefix(fields[name], []byte(`"`)) {
			t.Errorf("%s = %s, want a JSON string", name, fields[name])
		}
	}
	if v := resp.Header.Get("ETag"); v != `"1"` {
		t.Errorf("ETag = %q, want \"1\"", v)
	}
	if v := resp.Header.Get("Cache-Control"); v != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", v)
	}
	if !h.instrumentExists(t, symbol) {
		t.Errorf("instrument %s was not stored", symbol)
	}
	events := h.auditByTarget(t, got.ID)
	if len(events) != 1 || events[0].EventType != "transaction.created" || events[0].Actor != user.ID || events[0].Outcome != "success" {
		t.Errorf("audit = %+v, want one transaction.created by the caller", events)
	}
}

func TestCreateTradeReplaysTheSameKey(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	req := h.tradeRequest(h.freshSymbol(t))
	key := newKey(t)

	first, firstBody := b.createTrade(p.ID, key, req)
	again, againBody := b.createTrade(p.ID, key, req)
	if first.StatusCode != nethttp.StatusCreated || again.StatusCode != nethttp.StatusCreated || !bytes.Equal(firstBody, againBody) {
		t.Fatalf("first = %d %s, retry = %d %s, want the same 201 body twice", first.StatusCode, firstBody, again.StatusCode, againBody)
	}
	if again.Header.Get("ETag") != `"1"` {
		t.Errorf("retry ETag = %q, want \"1\"", again.Header.Get("ETag"))
	}
	if n := h.countRows(t, "transactions", p.ID); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
	if events := h.auditByTarget(t, decodeTransaction(t, firstBody).ID); len(events) != 1 {
		t.Errorf("audit events = %d, want 1 (a retry adds none)", len(events))
	}

	req["price"] = "201"
	resp, body := b.createTrade(p.ID, key, req)
	if resp.StatusCode != nethttp.StatusConflict || errorCode(t, body) != "idempotency_conflict" {
		t.Errorf("same key, other payload = %d %s, want 409 idempotency_conflict", resp.StatusCode, body)
	}
	if n := h.countRows(t, "transactions", p.ID); n != 1 {
		t.Errorf("transactions after the conflict = %d, want 1", n)
	}
}

func TestCreateTradeRejectsBadRequests(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	valid := func(mutate func(map[string]any)) map[string]any {
		m := map[string]any{}
		for k, v := range h.tradeRequest(symbol) {
			m[k] = v
		}
		if mutate != nil {
			mutate(m)
		}
		return m
	}

	cases := []struct {
		name        string
		portfolioID string
		key         string
		body        any
		code        string
	}{
		{"no Idempotency-Key", p.ID, "", valid(nil), "invalid_input"},
		{"Idempotency-Key over 128 characters", p.ID, strings.Repeat("k", 129), valid(nil), "invalid_input"},
		{"bad key is found before the body is read", p.ID, strings.Repeat("k", 129), "{not json", "invalid_input"},
		{"quantity as a JSON number", p.ID, "k", valid(func(m map[string]any) { m["quantity"] = 0.5 }), "malformed_json"},
		{"price as a JSON number", p.ID, "k", valid(func(m map[string]any) { m["price"] = 200 }), "malformed_json"},
		{"fee as a JSON number", p.ID, "k", valid(func(m map[string]any) { m["fee"] = 1 }), "malformed_json"},
		{"unknown field", p.ID, "k", valid(func(m map[string]any) { m["note"] = "x" }), "malformed_json"},
		{"not JSON", p.ID, "k", "{not json", "malformed_json"},
		{"missing fee", p.ID, "k", valid(func(m map[string]any) { delete(m, "fee") }), "invalid_input"},
		{"zero quantity", p.ID, "k", valid(func(m map[string]any) { m["quantity"] = "0" }), "invalid_input"},
		{"bad side", p.ID, "k", valid(func(m map[string]any) { m["side"] = "hold" }), "invalid_input"},
		{"bad symbol", p.ID, "k", valid(func(m map[string]any) { m["symbol"] = "AA PL" }), "invalid_input"},
		{"executed_at without a timezone", p.ID, "k", valid(func(m map[string]any) { m["executed_at"] = "2026-10-01T14:30:00" }), "invalid_input"},
		{"executed_at in the future", p.ID, "k", valid(func(m map[string]any) {
			m["executed_at"] = h.clock.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		}), "invalid_input"},
		{"portfolio_id is not a UUID", "abc", "k", valid(nil), "invalid_input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := tc.key
			if key == "k" {
				key = newKey(t)
			}
			resp, body := b.createTrade(tc.portfolioID, key, tc.body)
			if resp.StatusCode != nethttp.StatusBadRequest || errorCode(t, body) != tc.code {
				t.Errorf("create = %d %s, want 400 %s", resp.StatusCode, body, tc.code)
			}
			if n := h.countRows(t, "transactions", p.ID); n != 0 {
				t.Errorf("transactions = %d after a refused request, want 0", n)
			}
		})
	}
	if n := h.instruments.totalLookups(); n != 0 {
		t.Errorf("the provider was asked %d times for requests that were refused", n)
	}
}

func TestCreateTradeNeedsSessionCSRFAndOrigin(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	req := h.tradeRequest(h.freshSymbol(t))

	anonymous := h.newBrowser(t)
	if resp, body := anonymous.createTrade(p.ID, newKey(t), req); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("create without a session = %d %s, want 401", resp.StatusCode, body)
	}

	b.fetchCSRF()
	good := b.csrf
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
		t.Run(tc.name, func(t *testing.T) {
			b.csrf = tc.csrf
			headers := map[string]string{"Idempotency-Key": newKey(t)}
			for k, v := range tc.headers {
				headers[k] = v
			}
			resp, body := b.do(nethttp.MethodPost, transactionsPath(p.ID), req, headers)
			if resp.StatusCode != nethttp.StatusForbidden || errorCode(t, body) != "csrf_failed" {
				t.Errorf("create = %d %s, want 403 csrf_failed", resp.StatusCode, body)
			}
		})
	}
	if n := h.countRows(t, "transactions", p.ID); n != 0 {
		t.Errorf("transactions = %d after refused requests, want 0", n)
	}
}

func TestCreateTradeSymbolErrors(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")

	unknown := "UNK" + h.freshSymbol(t)[1:8]
	resp, body := b.createTrade(p.ID, newKey(t), h.tradeRequest(unknown))
	if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "unknown_symbol" {
		t.Errorf("unknown symbol = %d %s, want 422 unknown_symbol", resp.StatusCode, body)
	}
	if h.instrumentExists(t, unknown) {
		t.Errorf("unknown symbol %s was stored", unknown)
	}

	known := h.freshSymbol(t)
	b.mustCreateTrade(p.ID, h.tradeRequest(known))
	if n := h.instruments.lookups(known); n != 1 {
		t.Fatalf("lookups of %s = %d, want 1", known, n)
	}

	h.instruments.setDown(true)
	fresh := h.freshSymbol(t)
	resp, body = b.createTrade(p.ID, newKey(t), h.tradeRequest(fresh))
	if resp.StatusCode != nethttp.StatusServiceUnavailable || errorCode(t, body) != "dependency_unavailable" {
		t.Errorf("provider down, new symbol = %d %s, want 503 dependency_unavailable", resp.StatusCode, body)
	}
	if h.instrumentExists(t, fresh) {
		t.Errorf("symbol %s was stored although the provider was down", fresh)
	}
	resp, body = b.createTrade(p.ID, newKey(t), h.tradeRequest(known))
	if resp.StatusCode != nethttp.StatusCreated {
		t.Errorf("provider down, known symbol = %d %s, want 201", resp.StatusCode, body)
	}
	if n := h.instruments.lookups(known); n != 1 {
		t.Errorf("lookups of %s = %d, want still 1 (a stored symbol is not looked up)", known, n)
	}
	if n := h.countRows(t, "transactions", p.ID); n != 2 {
		t.Errorf("transactions = %d, want 2", n)
	}
}

func TestInstrumentLookupIsRateLimitedPerUser(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")

	var first string
	for i := range 30 {
		symbol := h.freshSymbol(t)
		if i == 0 {
			first = symbol
		}
		b.mustCreateTrade(p.ID, h.tradeRequest(symbol))
	}
	blocked := h.freshSymbol(t)
	resp, body := b.createTrade(p.ID, newKey(t), h.tradeRequest(blocked))
	if resp.StatusCode != nethttp.StatusTooManyRequests || errorCode(t, body) != "rate_limited" {
		t.Fatalf("31st new symbol = %d %s, want 429 rate_limited", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 has no Retry-After")
	}
	if h.instrumentExists(t, blocked) || h.instruments.lookups(blocked) != 0 {
		t.Errorf("the limited symbol %s reached the provider or the table", blocked)
	}
	if resp, body := b.createTrade(p.ID, newKey(t), h.tradeRequest(first)); resp.StatusCode != nethttp.StatusCreated {
		t.Errorf("stored symbol while limited = %d %s, want 201", resp.StatusCode, body)
	}

	other, _ := signedIn(t, h)
	otherPortfolio := other.mustCreatePortfolio("Mine", "")
	if resp, body := other.createTrade(otherPortfolio.ID, newKey(t), h.tradeRequest(h.freshSymbol(t))); resp.StatusCode != nethttp.StatusCreated {
		t.Errorf("another user's new symbol = %d %s, want 201", resp.StatusCode, body)
	}
}

func TestGetAndListTrades(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symA, symB := h.freshSymbol(t), h.freshSymbol(t)
	base := h.clock.Now().Add(-48 * time.Hour).Truncate(time.Second)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Minute) }

	var created []TransactionResponse // oldest first
	for i := range 25 {
		req := h.tradeRequest(symA)
		if i%2 == 1 {
			req["symbol"] = symB
		}
		if i == 24 {
			req["side"], req["quantity"] = "sell", "1"
		}
		req["executed_at"] = at(i).Format(time.RFC3339)
		created = append(created, b.mustCreateTrade(p.ID, req))
	}

	one := created[3]
	resp, body := b.getTrade(p.ID, one.ID)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("get = %d %s", resp.StatusCode, body)
	}
	if got := decodeTransaction(t, body); got != one {
		t.Errorf("get = %+v, want %+v", got, one)
	}
	if v := resp.Header.Get("ETag"); v != `"1"` {
		t.Errorf("ETag = %q, want \"1\"", v)
	}
	if v := resp.Header.Get("Cache-Control"); v != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", v)
	}
	if resp, body := b.getTrade(p.ID, mustTestID(t).String()); resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("get unknown = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	if resp, body := b.getTrade(p.ID, "abc"); resp.StatusCode != nethttp.StatusBadRequest || errorCode(t, body) != "invalid_input" {
		t.Errorf("get abc = %d %s, want 400 invalid_input", resp.StatusCode, body)
	}
	otherPortfolio := b.mustCreatePortfolio("Short term", "")
	if resp, body := b.getTrade(otherPortfolio.ID, one.ID); resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("get through another portfolio = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	if resp, _ := h.newBrowser(t).getTrade(p.ID, one.ID); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("get without a session = %d, want 401", resp.StatusCode)
	}
	stranger, _ := signedIn(t, h)
	if resp, body := stranger.getTrade(p.ID, one.ID); resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("get by a stranger = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	if resp, body := stranger.listTrades(p.ID, ""); resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("list by a stranger = %d %s, want 404 not_found", resp.StatusCode, body)
	}

	resp, body = b.listTrades(p.ID, "")
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("list = %d %s", resp.StatusCode, body)
	}
	first := decodeTransactionPage(t, body)
	if len(first.Data) != 20 || first.NextCursor == nil || first.Data[0].ID != created[24].ID || first.Data[19].ID != created[5].ID {
		t.Fatalf("first page: %d items, next_cursor %v, want 20 newest first and a cursor", len(first.Data), first.NextCursor)
	}
	if resp.Header.Get("ETag") != "" {
		t.Errorf("a list carries ETag %q, want none", resp.Header.Get("ETag"))
	}
	resp, body = b.listTrades(p.ID, "cursor="+url.QueryEscape(*first.NextCursor))
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("second page = %d %s", resp.StatusCode, body)
	}
	second := decodeTransactionPage(t, body)
	if len(second.Data) != 5 || second.NextCursor != nil {
		t.Fatalf("second page: %d items, next_cursor %v, want 5 and null", len(second.Data), second.NextCursor)
	}
	var seen, want []string
	for _, tr := range append(first.Data, second.Data...) {
		seen = append(seen, tr.ID)
	}
	for _, tr := range created {
		want = append(want, tr.ID)
	}
	slices.Sort(seen)
	slices.Sort(want)
	if !slices.Equal(seen, want) {
		t.Errorf("the two pages hold %d ids, want exactly the 25 trades once each", len(seen))
	}

	count := func(query string) int {
		t.Helper()
		resp, body := b.listTrades(p.ID, query)
		if resp.StatusCode != nethttp.StatusOK {
			t.Fatalf("list ?%s = %d %s", query, resp.StatusCode, body)
		}
		page := decodeTransactionPage(t, body)
		for _, tr := range page.Data {
			if strings.Contains(query, "symbol="+symA) && tr.Symbol != symA {
				t.Errorf("list ?%s returned a %s trade", query, tr.Symbol)
			}
		}
		return len(page.Data)
	}
	stamp := func(i int) string { return url.QueryEscape(at(i).Format(time.RFC3339)) }
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"symbol=" + symA + "&limit=100", 13},
		{"symbol=" + strings.ToLower(symB) + "&limit=100", 12},
		{"side=sell", 1},
		{"side=buy&limit=100", 24},
		{"from=" + stamp(10) + "&to=" + stamp(12), 3},
		{"from=" + stamp(24), 1},
		{"to=" + stamp(0), 1},
		{"symbol=&side=&from=&to=", 20},
	} {
		if got := count(tc.query); got != tc.want {
			t.Errorf("list ?%s returned %d trades, want %d", tc.query, got, tc.want)
		}
	}

	resp, body = b.listTrades(p.ID, "limit=5&symbol="+symA)
	filtered := decodeTransactionPage(t, body)
	if resp.StatusCode != nethttp.StatusOK || filtered.NextCursor == nil {
		t.Fatalf("filtered first page = %d %s", resp.StatusCode, body)
	}
	cursor := url.QueryEscape(*filtered.NextCursor)
	for _, query := range []string{
		"cursor=" + cursor + "&symbol=" + symB,
		"cursor=" + cursor,
		"cursor=" + cursor + "&symbol=" + symA + "&side=buy",
		"side=x",
		"symbol=AA%20PL",
		"from=" + stamp(12) + "&to=" + stamp(10),
		"from=yesterday",
		"to=2026-10-01T14:30:00",
		"limit=0",
		"limit=101",
	} {
		resp, body := b.listTrades(p.ID, query)
		if resp.StatusCode != nethttp.StatusBadRequest || errorCode(t, body) != "invalid_input" {
			t.Errorf("list ?%s = %d %s, want 400 invalid_input", query, resp.StatusCode, body)
		}
	}
	if resp, body := b.listTrades(p.ID, "cursor="+cursor+"&symbol="+symA); resp.StatusCode != nethttp.StatusOK {
		t.Errorf("the cursor with its own filter = %d %s, want 200", resp.StatusCode, body)
	}

	empty := b.mustCreatePortfolio("Empty", "")
	if resp, body := b.listTrades(empty.ID, ""); resp.StatusCode != nethttp.StatusOK || string(body) != `{"data":[],"next_cursor":null}` {
		t.Errorf("list of an empty portfolio = %d %s", resp.StatusCode, body)
	}

	resp, body = b.do(nethttp.MethodHead, transactionsPath(p.ID), nil, nil)
	if resp.StatusCode != nethttp.StatusOK || len(body) != 0 {
		t.Errorf("HEAD list = %d with %d body bytes, want 200 without a body", resp.StatusCode, len(body))
	}
	resp, body = b.do(nethttp.MethodHead, transactionsPath(p.ID)+"/"+one.ID, nil, nil)
	if resp.StatusCode != nethttp.StatusOK || len(body) != 0 || resp.Header.Get("ETag") != `"1"` {
		t.Errorf("HEAD one = %d, ETag %q, %d body bytes, want 200, \"1\", none", resp.StatusCode, resp.Header.Get("ETag"), len(body))
	}
	anon := h.newBrowser(t)
	for _, path := range []string{transactionsPath(p.ID), transactionsPath(p.ID) + "/" + one.ID} {
		if resp, _ := anon.do(nethttp.MethodHead, path, nil, nil); resp.StatusCode != nethttp.StatusUnauthorized {
			t.Errorf("HEAD %s without a session = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestTradesOfAnotherUsersPortfolioLookMissing(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	owner, _ := signedIn(t, h)
	p := owner.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	owner.mustCreateTrade(p.ID, h.tradeRequest(symbol))

	stranger, _ := signedIn(t, h)
	missing := mustTestID(t).String()
	for _, id := range []string{p.ID, missing} {
		resp, body := stranger.createTrade(id, newKey(t), h.tradeRequest(symbol))
		if resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
			t.Errorf("create in portfolio %s = %d %s, want 404 not_found", id, resp.StatusCode, body)
		}
	}
	if n := h.countRows(t, "transactions", p.ID); n != 1 {
		t.Errorf("transactions = %d, want only the owner's", n)
	}
	// A new symbol must not spend provider quota on a portfolio the caller does not own.
	fresh := h.freshSymbol(t)
	if resp, _ := stranger.createTrade(p.ID, newKey(t), h.tradeRequest(fresh)); resp.StatusCode != nethttp.StatusNotFound {
		t.Errorf("create with a new symbol in another user's portfolio = %d, want 404", resp.StatusCode)
	}
	if n := h.instruments.lookups(fresh); n != 0 {
		t.Errorf("the provider was asked %d times on behalf of a stranger", n)
	}
}

func TestTransactionRoutesAreOptional(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	if resp, body := b.listTrades(p.ID, ""); resp.StatusCode != nethttp.StatusNotFound {
		t.Errorf("list without the transaction service = %d %s, want 404", resp.StatusCode, body)
	}
}

func TestTransactionRoutesNeedTheAuthService(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewServer with transactions but no auth service did not panic")
		}
	}()
	log := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	NewServer(Options{Logger: log, Transactions: TransactionOptions{Service: unusedTransactionService{}}})
}

type unusedTransactionService struct{ port.TransactionService }
