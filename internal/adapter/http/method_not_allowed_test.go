package http

import (
	nethttp "net/http"
	"net/http/httptest"
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
