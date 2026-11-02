package http

import (
	"context"
	nethttp "net/http"
	"strings"
	"testing"
	"time"
)

// updatePortfolio asks for a token first. ifMatch "" sends no If-Match header; PUT replaces the
// whole portfolio, so the description is always sent ("" clears it).
func (b *browser) updatePortfolio(id, ifMatch, name, description string) (response, []byte) {
	b.t.Helper()
	return b.putPortfolio(id, ifMatch, map[string]string{"name": name, "description": description})
}

// putPortfolio is updatePortfolio with a body of the caller's choosing.
func (b *browser) putPortfolio(id, ifMatch string, body map[string]string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPut, "/api/v1/portfolios/"+id, body, ifMatchHeader(ifMatch))
}

func (b *browser) deletePortfolio(id, ifMatch string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodDelete, "/api/v1/portfolios/"+id, nil, ifMatchHeader(ifMatch))
}

func ifMatchHeader(value string) map[string]string {
	if value == "" {
		return nil
	}
	return map[string]string{"If-Match": value}
}

// portfolioState reads a portfolio with admin rights, so a test sees what the API left behind.
type portfolioState struct {
	Found   bool
	Name    string
	Version int
}

func (h *authHarness) portfolioState(t *testing.T, id string) portfolioState {
	t.Helper()
	var s portfolioState
	rows, err := h.admin.Query(context.Background(), `SELECT name, version FROM app.portfolios WHERE id = $1`, id)
	if err != nil {
		t.Fatalf("read portfolio: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		s.Found = true
		if err := rows.Scan(&s.Name, &s.Version); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return s
}

func (h *authHarness) countRows(t *testing.T, table, portfolioID string) int {
	t.Helper()
	var n int
	if err := h.admin.QueryRow(context.Background(), `SELECT count(*) FROM app.`+table+` WHERE portfolio_id = $1`, portfolioID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// seedTrade gives the portfolio a transaction in a fresh instrument, so it can no longer be deleted.
func (h *authHarness) seedTrade(t *testing.T, portfolioID string) {
	t.Helper()
	symbol := "Z" + strings.ToUpper(strings.ReplaceAll(mustTestID(t).String(), "-", ""))[:8]
	instrumentID := mustTestID(t).String()
	now := time.Now().UTC()
	_, err := h.admin.Exec(context.Background(),
		`INSERT INTO app.instruments (id, symbol, name, exchange, asset_class, status, source, verified_at, created_at, updated_at)
		 VALUES ($1, $2, 'Test Corp', 'NASDAQ', 'us_equity', 'active', 'demo', $3, $3, $3)`, instrumentID, symbol, now)
	if err != nil {
		t.Fatalf("seed instrument: %v", err)
	}
	_, err = h.admin.Exec(context.Background(),
		`INSERT INTO app.transactions (id, portfolio_id, instrument_id, side, quantity, price, fee, executed_at, created_at, updated_at)
		 VALUES ($1, $2, $3, 'buy', 1, 10, 0, $4, $4, $4)`, mustTestID(t).String(), portfolioID, instrumentID, now)
	if err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
}

// seedIdempotencyKey stores a key of the portfolio's owner, as a retried POST of a trade would.
func (h *authHarness) seedIdempotencyKey(t *testing.T, userID, portfolioID string) {
	t.Helper()
	now := time.Now().UTC()
	_, err := h.admin.Exec(context.Background(),
		`INSERT INTO app.idempotency_keys (portfolio_id, user_id, operation, key, request_hash, result, created_at, expires_at)
		 VALUES ($1, $2, 'transactions.create', $3, decode(repeat('ab', 32), 'hex'), '{}'::jsonb, $4, $5)`,
		portfolioID, userID, "key-"+mustTestID(t).String(), now, now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("seed idempotency key: %v", err)
	}
}

func TestUpdatePortfolio(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, user := signedIn(t, h)
	created := b.mustCreatePortfolio("Long term", "first")
	h.clock.Advance(time.Minute)

	resp, body := b.updatePortfolio(created.ID, `"1"`, "Long term", "second")
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, body)
	}
	got := decodePortfolio(t, body)
	if got.Version != 2 || got.Description != "second" || got.Name != "Long term" || !got.UpdatedAt.After(created.UpdatedAt) || !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("updated portfolio = %+v, created %+v", got, created)
	}
	if v := resp.Header.Get("ETag"); v != `"2"` {
		t.Errorf("ETag = %q, want \"2\"", v)
	}
	if v := resp.Header.Get("Cache-Control"); v != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", v)
	}

	events := h.auditByTarget(t, created.ID)
	if len(events) != 2 || events[1].EventType != "portfolio.updated" || events[1].Actor != user.ID {
		t.Fatalf("audit = %+v, want portfolio.created then portfolio.updated by the caller", events)
	}
	if changed, _ := events[1].Metadata["changed"].([]any); len(changed) != 1 || changed[0] != "description" {
		t.Errorf("changed = %v, want [description]", events[1].Metadata["changed"])
	}
}

// PUT replaces the portfolio. A body without description would silently erase the stored one, so it
// is refused; sending "" is the explicit way to clear it.
func TestUpdatePortfolioRequiresTheDescription(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	created := b.mustCreatePortfolio("Long term", "keep me")
	eventsBefore := len(h.auditByTarget(t, created.ID))

	resp, body := b.putPortfolio(created.ID, `"1"`, map[string]string{"name": "Renamed"})
	if resp.StatusCode != nethttp.StatusBadRequest || errorCode(t, body) != "invalid_input" || !strings.Contains(string(body), "description") {
		t.Fatalf("PUT without description = %d %s, want 400 invalid_input naming description", resp.StatusCode, body)
	}
	got, _ := b.do(nethttp.MethodGet, "/api/v1/portfolios/"+created.ID, nil, nil)
	if got.Header.Get("ETag") != `"1"` {
		t.Errorf("ETag after the refused PUT = %q, want \"1\"", got.Header.Get("ETag"))
	}
	if state := h.portfolioState(t, created.ID); state.Name != "Long term" || state.Version != 1 {
		t.Errorf("portfolio after the refused PUT = %+v, want Long term at version 1", state)
	}
	if n := len(h.auditByTarget(t, created.ID)); n != eventsBefore {
		t.Errorf("audit events = %d, want %d", n, eventsBefore)
	}

	resp, body = b.putPortfolio(created.ID, `"1"`, map[string]string{"name": "Renamed", "description": ""})
	if resp.StatusCode != nethttp.StatusOK || decodePortfolio(t, body).Description != "" {
		t.Errorf("PUT with description \"\" = %d %s, want 200 with the description cleared", resp.StatusCode, body)
	}
}

func TestUpdatePortfolioPreconditions(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	created := b.mustCreatePortfolio("Long term", "")
	if resp, body := b.updatePortfolio(created.ID, `"1"`, "Long term", "now at version 2"); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("update to version 2 = %d %s", resp.StatusCode, body)
	}
	eventsBefore := len(h.auditByTarget(t, created.ID))

	cases := []struct {
		name, ifMatch string
		status        int
		code          string
	}{
		{"no If-Match", "", nethttp.StatusPreconditionRequired, "precondition_required"},
		{"stale version", `"1"`, nethttp.StatusPreconditionFailed, "version_conflict"},
		{"wildcard", `*`, nethttp.StatusBadRequest, "invalid_input"},
		{"weak validator", `W/"2"`, nethttp.StatusBadRequest, "invalid_input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := b.updatePortfolio(created.ID, tc.ifMatch, "Renamed", "other")
			if resp.StatusCode != tc.status || errorCode(t, body) != tc.code {
				t.Errorf("update = %d %s, want %d %s", resp.StatusCode, body, tc.status, tc.code)
			}
			if state := h.portfolioState(t, created.ID); state.Name != "Long term" || state.Version != 2 {
				t.Errorf("portfolio after the refused update = %+v, want Long term at version 2", state)
			}
			if n := len(h.auditByTarget(t, created.ID)); n != eventsBefore {
				t.Errorf("audit events = %d, want %d (a refused update writes none)", n, eventsBefore)
			}
		})
	}
}

func TestRenamePortfolioCaseRules(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	long := b.mustCreatePortfolio("Long", "")
	b.mustCreatePortfolio("Short", "")

	resp, body := b.updatePortfolio(long.ID, `"1"`, "long", "")
	if resp.StatusCode != nethttp.StatusOK || decodePortfolio(t, body).Name != "long" {
		t.Fatalf("rename Long to long = %d %s, want 200", resp.StatusCode, body)
	}
	resp, body = b.updatePortfolio(long.ID, `"2"`, "SHORT", "")
	if resp.StatusCode != nethttp.StatusConflict || errorCode(t, body) != "portfolio_name_conflict" {
		t.Errorf("rename to another portfolio's name = %d %s, want 409 portfolio_name_conflict", resp.StatusCode, body)
	}
	if state := h.portfolioState(t, long.ID); state.Name != "long" || state.Version != 2 {
		t.Errorf("portfolio after the name conflict = %+v, want long at version 2", state)
	}
}

func TestDeleteEmptyPortfolio(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, user := signedIn(t, h)
	created := b.mustCreatePortfolio("Long term", "")

	resp, body := b.deletePortfolio(created.ID, `"1"`)
	if resp.StatusCode != nethttp.StatusNoContent || len(body) != 0 {
		t.Fatalf("delete = %d %s, want 204 with no body", resp.StatusCode, body)
	}
	if resp, body := b.getPortfolio(created.ID); resp.StatusCode != nethttp.StatusNotFound {
		t.Errorf("get after delete = %d %s, want 404", resp.StatusCode, body)
	}
	events := h.auditByTarget(t, created.ID)
	if len(events) != 2 || events[1].EventType != "portfolio.deleted" || events[1].Actor != user.ID {
		t.Errorf("audit = %+v, want portfolio.created then portfolio.deleted by the caller", events)
	}
	if resp, body := b.deletePortfolio(created.ID, `"1"`); resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("second delete = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	if n := len(h.auditByTarget(t, created.ID)); n != 2 {
		t.Errorf("audit events after the second delete = %d, want 2 (an unknown portfolio is not an access denial)", n)
	}
}

func TestDeletePortfolioPreconditions(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	created := b.mustCreatePortfolio("Long term", "")
	if resp, body := b.updatePortfolio(created.ID, `"1"`, "Long term", "version 2"); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, body)
	}

	resp, body := b.deletePortfolio(created.ID, "")
	if resp.StatusCode != nethttp.StatusPreconditionRequired || errorCode(t, body) != "precondition_required" {
		t.Errorf("delete without If-Match = %d %s, want 428", resp.StatusCode, body)
	}
	resp, body = b.deletePortfolio(created.ID, `"1"`)
	if resp.StatusCode != nethttp.StatusPreconditionFailed || errorCode(t, body) != "version_conflict" {
		t.Errorf("delete with a stale version = %d %s, want 412", resp.StatusCode, body)
	}
	if state := h.portfolioState(t, created.ID); !state.Found || state.Version != 2 {
		t.Errorf("portfolio after the refused deletes = %+v, want it kept at version 2", state)
	}
}

func TestDeletePortfolioWithTransactionsIsRefused(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, user := signedIn(t, h)
	created := b.mustCreatePortfolio("Long term", "")
	h.seedTrade(t, created.ID)
	h.seedIdempotencyKey(t, user.ID, created.ID)

	resp, body := b.deletePortfolio(created.ID, `"1"`)
	if resp.StatusCode != nethttp.StatusConflict || errorCode(t, body) != "portfolio_not_empty" {
		t.Fatalf("delete = %d %s, want 409 portfolio_not_empty", resp.StatusCode, body)
	}
	if state := h.portfolioState(t, created.ID); !state.Found {
		t.Error("the portfolio was deleted")
	}
	if n := h.countRows(t, "transactions", created.ID); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
	if n := h.countRows(t, "idempotency_keys", created.ID); n != 1 {
		t.Errorf("idempotency keys = %d, want 1 (the refused delete rolls back with its keys)", n)
	}
	for _, e := range h.auditByTarget(t, created.ID) {
		if e.EventType == "portfolio.deleted" {
			t.Errorf("audit has a portfolio.deleted event for a portfolio that was kept")
		}
	}
}

func TestDeletePortfolioClearsLeftoverIdempotencyKeys(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, user := signedIn(t, h)
	created := b.mustCreatePortfolio("Long term", "")
	h.seedIdempotencyKey(t, user.ID, created.ID)

	if resp, body := b.deletePortfolio(created.ID, `"1"`); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("delete = %d %s, want 204", resp.StatusCode, body)
	}
	if n := h.countRows(t, "idempotency_keys", created.ID); n != 0 {
		t.Errorf("idempotency keys = %d, want 0", n)
	}
}

// TestUpdateAndDeletePortfolioNeedCSRFAndOrigin: PUT and DELETE change data, so they are held to the
// same CSRF and Origin checks as POST, and a refused call changes nothing.
func TestUpdateAndDeletePortfolioNeedCSRFAndOrigin(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	created := b.mustCreatePortfolio("Long term", "")
	path := "/api/v1/portfolios/" + created.ID
	b.fetchCSRF()
	good := b.csrf
	// A genuine token, issued by this API to somebody else's session: well formed and correctly
	// signed, refused only because it is bound to the other session.
	other, _ := signedIn(t, h)
	other.fetchCSRF()
	othersToken := other.csrf

	cases := []struct {
		name    string
		csrf    string
		headers map[string]string
	}{
		{"missing token", "", nil},
		{"token bound to another session", othersToken, nil},
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
					body = map[string]string{"name": "Taken over", "description": ""}
				}
				resp, data := b.do(method, path, body, headers)
				if resp.StatusCode != nethttp.StatusForbidden || errorCode(t, data) != "csrf_failed" {
					t.Errorf("%s = %d %s, want 403 csrf_failed", method, resp.StatusCode, data)
				}
				if state := h.portfolioState(t, created.ID); !state.Found || state.Name != "Long term" || state.Version != 1 {
					t.Errorf("portfolio after the refused %s = %+v, want it untouched", method, state)
				}
			})
		}
	}
}
