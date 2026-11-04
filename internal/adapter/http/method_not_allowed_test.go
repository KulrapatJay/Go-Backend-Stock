package http

import (
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnownPathWithAnotherMethodIs405WithAllow(t *testing.T) {
	app, logs := newTestServer(t, fakeReadiness{})

	resp, body := doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodPost, "/healthz", nil))
	if resp.StatusCode != nethttp.StatusMethodNotAllowed {
		t.Fatalf("POST /healthz = %d, want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if code := decodeError(t, body).Code; code != "method_not_allowed" {
		t.Errorf("code = %q, want method_not_allowed", code)
	}
	for _, e := range accessEntries(t, logs) {
		if e["route"] != "unmatched" {
			t.Errorf("access log route = %v, want unmatched (never the raw path)", e["route"])
		}
	}

	resp, _ = doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/no-such-path", nil))
	if resp.StatusCode != nethttp.StatusNotFound || resp.Header.Get("Allow") != "" {
		t.Errorf("unknown path = %d Allow=%q, want 404 without Allow", resp.StatusCode, resp.Header.Get("Allow"))
	}

	resp, _ = doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodHead, "/healthz", nil))
	if resp.StatusCode != nethttp.StatusOK {
		t.Errorf("HEAD /healthz = %d, want 200", resp.StatusCode)
	}
}

func TestParameterRouteWithAnotherMethodIs405WithAllow(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	id := b.mustCreatePortfolio("Long term", "").ID
	path := "/api/v1/portfolios/" + id

	for _, method := range []string{nethttp.MethodPatch, nethttp.MethodPost} {
		resp, body := b.do(method, path, nil, nil)
		if resp.StatusCode != nethttp.StatusMethodNotAllowed {
			t.Fatalf("%s %s = %d %s, want 405", method, path, resp.StatusCode, body)
		}
		if got := resp.Header.Get("Allow"); got != "DELETE, GET, HEAD, PUT" {
			t.Errorf("%s Allow = %q, want %q", method, got, "DELETE, GET, HEAD, PUT")
		}
		if code := decodeError(t, body).Code; code != "method_not_allowed" {
			t.Errorf("%s code = %q, want method_not_allowed", method, code)
		}
	}

	// The access log never records the raw path, so the id stays out of it and the route is unmatched.
	found := false
	for _, line := range strings.Split(h.logs.String(), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) != nil || e["msg"] != "http request" || e["status"] != float64(nethttp.StatusMethodNotAllowed) {
			continue
		}
		found = true
		if e["route"] != "unmatched" {
			t.Errorf("access log route = %v, want unmatched", e["route"])
		}
	}
	if !found {
		t.Error("no access log entry with status 405")
	}

	resp, _ := b.do(nethttp.MethodGet, path+"/nope", nil, nil)
	if resp.StatusCode != nethttp.StatusNotFound || resp.Header.Get("Allow") != "" {
		t.Errorf("GET %s/nope = %d Allow=%q, want 404 without Allow", path, resp.StatusCode, resp.Header.Get("Allow"))
	}
}

func TestHeadOnGoogleCallbackDoesNotRunIt(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	resp, _ := b.do(nethttp.MethodHead, "/api/v1/auth/google/callback?state=x&code=y", nil, nil)
	if resp.StatusCode != nethttp.StatusNotFound {
		t.Errorf("HEAD /api/v1/auth/google/callback = %d, want 404", resp.StatusCode)
	}
	if strings.Contains(h.logs.String(), "auth.oidc_flow_not_found") {
		t.Error("the callback handler ran for a HEAD request")
	}
}
