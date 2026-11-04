package http

import (
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/url"
	"slices"
	"testing"
	"time"
)

func TestCreatePortfolio(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, user := signedIn(t, h)

	resp, body := b.createPortfolio("  Long term  ", "")
	if resp.StatusCode != nethttp.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	got := decodePortfolio(t, body)
	if got.Name != "Long term" || got.Description != "" || got.Currency != "USD" || got.Version != 1 || got.ID == "" ||
		got.CreatedAt.IsZero() || !got.CreatedAt.Equal(got.UpdatedAt) {
		t.Errorf("portfolio = %+v", got)
	}
	if v := resp.Header.Get("ETag"); v != `"1"` {
		t.Errorf("ETag = %q, want \"1\"", v)
	}
	if v := resp.Header.Get("Cache-Control"); v != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", v)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"id", "name", "description", "currency", "version", "created_at", "updated_at"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("body has no %q: %s", name, body)
		}
	}
	if len(fields) != 7 {
		t.Errorf("body has %d fields, want 7: %s", len(fields), body)
	}

	events := h.auditByTarget(t, got.ID)
	if len(events) != 1 || events[0].EventType != "portfolio.created" || events[0].Actor != user.ID || events[0].Outcome != "success" {
		t.Errorf("audit = %+v, want one portfolio.created by the caller", events)
	}
}

func TestCreatePortfolioNeedsSessionAndCSRF(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())

	anonymous := h.newBrowser(t)
	if resp, body := anonymous.createPortfolio("Long term", ""); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("create without a session = %d %s, want 401", resp.StatusCode, body)
	}

	b, _ := signedIn(t, h)
	// The token b holds was issued before login, so it is bound to the old nonce, not the session.
	resp, body := b.do(nethttp.MethodPost, "/api/v1/portfolios", map[string]string{"name": "Long term"}, nil)
	if resp.StatusCode != nethttp.StatusForbidden || errorCode(t, body) != "csrf_failed" {
		t.Errorf("create with a stale CSRF token = %d %s, want 403 csrf_failed", resp.StatusCode, body)
	}
	if resp, body := b.listPortfolios(""); resp.StatusCode != nethttp.StatusOK || decodePortfolioPage(t, body).Data == nil {
		t.Errorf("list after the refused create = %d %s", resp.StatusCode, body)
	}
}

func TestCreatePortfolioNameRules(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	b.mustCreatePortfolio("Long term", "")

	resp, body := b.createPortfolio("long TERM", "")
	if resp.StatusCode != nethttp.StatusConflict || errorCode(t, body) != "portfolio_name_conflict" {
		t.Errorf("same name in another case = %d %s, want 409 portfolio_name_conflict", resp.StatusCode, body)
	}
	other, _ := signedIn(t, h)
	if resp, body := other.createPortfolio("Long term", ""); resp.StatusCode != nethttp.StatusCreated {
		t.Errorf("another user using the name = %d %s, want 201", resp.StatusCode, body)
	}
	resp, body = b.createPortfolio("   ", "")
	if resp.StatusCode != nethttp.StatusBadRequest || errorCode(t, body) != "invalid_input" {
		t.Errorf("blank name = %d %s, want 400 invalid_input", resp.StatusCode, body)
	}
}

func TestGetPortfolio(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	created := b.mustCreatePortfolio("Long term", "Buy and hold")

	resp, body := b.getPortfolio(created.ID)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("get = %d %s", resp.StatusCode, body)
	}
	if got := decodePortfolio(t, body); got != created {
		t.Errorf("get = %+v, want %+v", got, created)
	}
	if v := resp.Header.Get("ETag"); v != `"1"` {
		t.Errorf("ETag = %q, want \"1\"", v)
	}
	if v := resp.Header.Get("Cache-Control"); v != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", v)
	}

	if resp, body := b.getPortfolio(mustTestID(t).String()); resp.StatusCode != nethttp.StatusNotFound || errorCode(t, body) != "not_found" {
		t.Errorf("get unknown = %d %s, want 404 not_found", resp.StatusCode, body)
	}
	if resp, body := b.getPortfolio("abc"); resp.StatusCode != nethttp.StatusBadRequest || errorCode(t, body) != "invalid_input" {
		t.Errorf("get abc = %d %s, want 400 invalid_input", resp.StatusCode, body)
	}
	if resp, _ := h.newBrowser(t).getPortfolio(created.ID); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("get without a session = %d, want 401", resp.StatusCode)
	}
}

func TestListPortfoliosPagesNewestFirst(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	var created []string // oldest first
	for i := range 25 {
		h.clock.Advance(time.Second)
		created = append(created, b.mustCreatePortfolio(fmt.Sprintf("Portfolio %02d", i), "").ID)
	}
	other, _ := signedIn(t, h)
	other.mustCreatePortfolio("Not yours", "")

	resp, body := b.listPortfolios("")
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("list = %d %s", resp.StatusCode, body)
	}
	first := decodePortfolioPage(t, body)
	if len(first.Data) != 20 || first.NextCursor == nil || first.Data[0].ID != created[24] || first.Data[19].ID != created[5] {
		t.Fatalf("first page: %d items, next_cursor %v, want 20 newest first and a cursor", len(first.Data), first.NextCursor)
	}
	if resp.Header.Get("ETag") != "" {
		t.Errorf("a list carries ETag %q, want none", resp.Header.Get("ETag"))
	}

	resp, body = b.listPortfolios("cursor=" + url.QueryEscape(*first.NextCursor))
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("second page = %d %s", resp.StatusCode, body)
	}
	second := decodePortfolioPage(t, body)
	if len(second.Data) != 5 || second.NextCursor != nil {
		t.Fatalf("second page: %d items, next_cursor %v, want 5 and null", len(second.Data), second.NextCursor)
	}
	var seen []string
	for _, p := range append(first.Data, second.Data...) {
		seen = append(seen, p.ID)
	}
	slices.Sort(seen)
	if want := slices.Sorted(slices.Values(created)); !slices.Equal(seen, want) {
		t.Errorf("the two pages hold %v, want exactly the 25 portfolios of the caller", seen)
	}

	for _, query := range []string{"limit=101", "limit=0", "cursor=garbage"} {
		resp, body := b.listPortfolios(query)
		if resp.StatusCode != nethttp.StatusBadRequest || errorCode(t, body) != "invalid_input" {
			t.Errorf("list ?%s = %d %s, want 400 invalid_input", query, resp.StatusCode, body)
		}
	}

	empty, _ := signedIn(t, h)
	if resp, body := empty.listPortfolios(""); resp.StatusCode != nethttp.StatusOK || string(body) != `{"data":[],"next_cursor":null}` {
		t.Errorf("list of a user without portfolios = %d %s", resp.StatusCode, body)
	}
}

func TestHeadOnPortfolioRoutes(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	created := b.mustCreatePortfolio("Long term", "")

	resp, body := b.do(nethttp.MethodHead, "/api/v1/portfolios", nil, nil)
	if resp.StatusCode != nethttp.StatusOK || len(body) != 0 {
		t.Errorf("HEAD list = %d with %d body bytes, want 200 without a body", resp.StatusCode, len(body))
	}
	resp, body = b.do(nethttp.MethodHead, "/api/v1/portfolios/"+created.ID, nil, nil)
	if resp.StatusCode != nethttp.StatusOK || len(body) != 0 {
		t.Errorf("HEAD one = %d with %d body bytes, want 200 without a body", resp.StatusCode, len(body))
	}
	if v := resp.Header.Get("ETag"); v != `"1"` {
		t.Errorf("HEAD one ETag = %q, want \"1\"", v)
	}

	anon := h.newBrowser(t)
	for _, path := range []string{"/api/v1/portfolios", "/api/v1/portfolios/" + created.ID} {
		if resp, _ := anon.do(nethttp.MethodHead, path, nil, nil); resp.StatusCode != nethttp.StatusUnauthorized {
			t.Errorf("HEAD %s without a session = %d, want 401", path, resp.StatusCode)
		}
	}
}

// errorCode reads error.code from an ErrorResponse body.
func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e ErrorResponse
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("error body %s: %v", body, err)
	}
	return e.Error.Code
}
