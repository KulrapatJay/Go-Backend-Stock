package http

import (
	"encoding/json"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

func TestAuthConfigReportsWhetherGoogleIsOn(t *testing.T) {
	for name, h := range map[string]*authHarness{
		"off": newAuthHarness(t),
		"on":  newAuthHarness(t, withIdentityProvider(newFakeIdentityProvider())),
	} {
		resp, body := h.newBrowser(t).do(nethttp.MethodGet, "/api/v1/auth/config", nil, nil)
		var out AuthConfigResponse
		if err := json.Unmarshal(body, &out); err != nil || resp.StatusCode != nethttp.StatusOK {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, body)
		}
		if out.GoogleEnabled != (name == "on") || !strings.Contains(string(body), `"google_enabled"`) {
			t.Errorf("%s: body %s", name, body)
		}
	}
}

func TestGoogleWhenDisabledIs503(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	resp, body := b.startGoogle()
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "feature_disabled" {
		t.Errorf("start = %d %s, want 503 feature_disabled", resp.StatusCode, body)
	}
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")
	resp, body = b.linkGoogle()
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "feature_disabled" {
		t.Errorf("link = %d %s, want 503 feature_disabled", resp.StatusCode, body)
	}
}

func TestGoogleStartReturnsAnAuthorizationURLAndABindingCookie(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b := h.newBrowser(t)

	resp, body := b.startGoogle()
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("start = %d %s", resp.StatusCode, body)
	}
	out := decodeStart(t, body)
	req := f.lastRequest(t)
	if !strings.Contains(out.AuthorizationURL, "state="+req.State) || resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("url = %s, Cache-Control = %q", out.AuthorizationURL, resp.Header.Get("Cache-Control"))
	}
	raw := strings.ToLower(strings.Join(resp.Header.Values("Set-Cookie"), "\n"))
	for _, want := range []string{"oidc_binding=", "httponly", "samesite=lax", "path=/", "expires="} {
		if !strings.Contains(raw, want) {
			t.Errorf("Set-Cookie %q lacks %q", raw, want)
		}
	}
	binding := b.jar["oidc_binding"]
	codec, err := service.NewOIDCCodec([]byte(testOIDCSecret))
	if err != nil {
		t.Fatal(err)
	}
	if !service.ValidOIDCValue(binding) || req.CodeChallenge != service.CodeChallenge(codec.Verifier(req.State, binding)) {
		t.Error("the challenge must come from the state and the binding the browser holds")
	}
	flow, ok := h.flowByState(t, req.State)
	if !ok || flow.Purpose != "login" || flow.UserID != "" || flow.Lifetime != 10*time.Minute || flow.Consumed {
		t.Errorf("flow = %+v (found %v), want an open 10-minute login flow", flow, ok)
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("starting a flow writes no audit, got %v", eventTypes(rows))
	}
}

func TestGoogleLinkNeedsASignedInUserAndBindsTheFlow(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b := h.newBrowser(t)
	resp, body := b.linkGoogle()
	if resp.StatusCode != nethttp.StatusUnauthorized || decodeError(t, body).Code != "unauthenticated" {
		t.Fatalf("anonymous link = %d %s, want 401", resp.StatusCode, body)
	}
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	if resp, body := b.linkGoogle(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("link = %d %s", resp.StatusCode, body)
	}
	if flow, ok := h.flowByState(t, f.lastRequest(t).State); !ok || flow.Purpose != "link" || flow.UserID != u.ID {
		t.Errorf("flow = %+v, want a link flow of the signed-in user", flow)
	}
}

func TestGoogleStartNeedsCSRF(t *testing.T) {
	h := newAuthHarness(t, withIdentityProvider(newFakeIdentityProvider()))
	b := h.newBrowser(t)
	b.fetchCSRF()
	resp, body := b.do(nethttp.MethodPost, "/api/v1/auth/google/start", nil, map[string]string{"X-CSRF-Token": ""})
	if resp.StatusCode != nethttp.StatusForbidden || decodeError(t, body).Code != "csrf_failed" {
		t.Errorf("start without a token = %d %s, want 403 csrf_failed", resp.StatusCode, body)
	}
}

func TestGoogleStartWhenTheProviderIsDownIs503(t *testing.T) {
	f := newFakeIdentityProvider()
	f.setURLError(domain.NewError(domain.CodeDependencyUnavailable, "identity provider is unavailable"))
	h := newAuthHarness(t, withIdentityProvider(f))
	resp, body := h.newBrowser(t).startGoogle()
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "dependency_unavailable" {
		t.Fatalf("start = %d %s, want 503 dependency_unavailable", resp.StatusCode, body)
	}
	if _, ok := h.flowByState(t, f.lastRequest(t).State); ok {
		t.Error("no flow may be stored when the provider is down")
	}
}

func TestGoogleStartIsRateLimitedPerIP(t *testing.T) {
	h := newAuthHarness(t, withIdentityProvider(newFakeIdentityProvider()))
	b := h.newBrowser(t)
	for i := range 20 {
		if resp, body := b.startGoogle(); resp.StatusCode != nethttp.StatusOK {
			t.Fatalf("start %d = %d %s", i+1, resp.StatusCode, body)
		}
	}
	resp, body := b.startGoogle()
	if resp.StatusCode != nethttp.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("start 21 = %d %s, want 429 with Retry-After", resp.StatusCode, body)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "security.rate_limited" || rows[0].Metadata["scope"] != "google_start_ip" {
		t.Errorf("audit = %+v, want security.rate_limited for google_start_ip", rows)
	}
}
