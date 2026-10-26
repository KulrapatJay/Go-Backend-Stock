package http

import (
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

func cookieNamed(resp response, name string) *nethttp.Cookie {
	for _, c := range (&nethttp.Response{Header: resp.Header}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func getCSRF(t *testing.T, app *fiber.App, cookies ...*nethttp.Cookie) (response, CSRFResponse) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/v1/auth/csrf", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, body := doRequest(t, app, req)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET /api/v1/auth/csrf = %d %s", resp.StatusCode, body)
	}
	var out CSRFResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return resp, out
}

func TestCSRFTokenBeforeLoginSetsANonceCookie(t *testing.T) {
	app, codec := newAuthTestServer(t, &fakeAuthService{}, false)

	resp, out := getCSRF(t, app)
	nonce := cookieNamed(resp, "csrf_nonce")
	if nonce == nil || !middleware.ValidNonce(nonce.Value) {
		t.Fatalf("Set-Cookie = %v, want a csrf_nonce cookie with a fresh nonce", resp.Header.Values("Set-Cookie"))
	}
	raw := strings.ToLower(strings.Join(resp.Header.Values("Set-Cookie"), "\n"))
	for _, want := range []string{"httponly", "samesite=lax", "path=/", "expires="} {
		if !strings.Contains(raw, want) {
			t.Errorf("nonce cookie %q lacks %q", raw, want)
		}
	}
	if strings.Contains(raw, "secure") || strings.Contains(raw, "domain=") {
		t.Errorf("an http origin must not set Secure or Domain: %q", raw)
	}
	if err := codec.Verify(out.CSRFToken, service.PreLoginCSRFBinding(nonce.Value)); err != nil {
		t.Errorf("token does not verify against the cookie's nonce: %v", err)
	}
	if out.ExpiresAt.IsZero() || out.ExpiresAt.Location().String() != "UTC" {
		t.Errorf("expires_at = %v, want a UTC time", out.ExpiresAt)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store (a CSRF token and nonce cookie must not be cached)", got)
	}

	// The same browser asks again: the nonce is kept (and its cookie renewed) so older tabs keep working.
	resp2, out2 := getCSRF(t, app, &nethttp.Cookie{Name: "csrf_nonce", Value: nonce.Value})
	if again := cookieNamed(resp2, "csrf_nonce"); again == nil || again.Value != nonce.Value {
		t.Errorf("second call cookie = %v, want the same nonce renewed", again)
	}
	if err := codec.Verify(out2.CSRFToken, service.PreLoginCSRFBinding(nonce.Value)); err != nil {
		t.Errorf("second token does not verify: %v", err)
	}

	// A forged or broken nonce cookie is replaced, not trusted.
	resp3, _ := getCSRF(t, app, &nethttp.Cookie{Name: "csrf_nonce", Value: "not-a-nonce"})
	if fresh := cookieNamed(resp3, "csrf_nonce"); fresh == nil || fresh.Value == "not-a-nonce" || !middleware.ValidNonce(fresh.Value) {
		t.Errorf("broken nonce cookie was kept: %v", fresh)
	}
}

func TestCSRFTokenWhenSignedInIsBoundToTheSession(t *testing.T) {
	principal := domain.Principal{UserID: mustTestID(t), SessionID: mustTestID(t)}
	app, codec := newAuthTestServer(t, &fakeAuthService{principals: map[string]domain.Principal{"session-token": principal}}, false)

	resp, out := getCSRF(t, app, &nethttp.Cookie{Name: "session", Value: "session-token"})
	if err := codec.Verify(out.CSRFToken, service.SessionCSRFBinding(principal.SessionID)); err != nil {
		t.Errorf("token is not bound to the session: %v", err)
	}
	if cookieNamed(resp, "csrf_nonce") != nil {
		t.Error("a signed-in call must not set a nonce cookie")
	}
}

func TestSecureOriginUsesHostPrefixedSecureCookies(t *testing.T) {
	app, _ := newAuthTestServer(t, &fakeAuthService{}, true)
	resp, _ := getCSRF(t, app)
	c := cookieNamed(resp, "__Host-csrf_nonce")
	if c == nil || !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
		t.Errorf("cookie = %+v, want __Host-csrf_nonce with Secure, HttpOnly, Path=/ and no Domain", c)
	}
}

func TestCSRFTokenWhenTheSessionStoreIsDown(t *testing.T) {
	down := domain.NewError(domain.CodeDependencyUnavailable, "session store is unavailable")
	app, _ := newAuthTestServer(t, &fakeAuthService{err: down}, false)
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/v1/auth/csrf", nil)
	req.AddCookie(&nethttp.Cookie{Name: "session", Value: "anything"})
	resp, body := doRequest(t, app, req)
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "dependency_unavailable" {
		t.Errorf("got %d %s, want 503 dependency_unavailable (an outage must not look like a logout)", resp.StatusCode, body)
	}
}

// csrfTestApp is a minimal app with the session and CSRF middleware in the order the server uses.
func csrfTestApp(t *testing.T, auth middleware.Authenticator, codec *service.CSRFCodec) *fiber.App {
	t.Helper()
	log, _ := newTestLogger()
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(log)})
	app.Use(middleware.RequestID())
	ok := func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) }
	app.Post("/x", middleware.ResolveSession(auth, "session"), middleware.CSRF(codec, testOrigin, "csrf_nonce"), ok)
	app.Get("/x", middleware.ResolveSession(auth, "session"), middleware.CSRF(codec, testOrigin, "csrf_nonce"), ok)
	app.Get("/private", middleware.ResolveSession(auth, "session"), middleware.RequireAuth(), func(c fiber.Ctx) error {
		p, _ := middleware.GetPrincipal(c)
		return c.SendString(p.UserID.String())
	})
	return app
}

func TestCSRFMiddleware(t *testing.T) {
	codec := newTestCSRF(t, newTestClock(time.Now()))
	principal := domain.Principal{UserID: mustTestID(t), SessionID: mustTestID(t)}
	auth := &fakeAuthService{principals: map[string]domain.Principal{"session-token": principal}}
	app := csrfTestApp(t, auth, codec)

	nonce, err := service.NewCSRFNonce()
	if err != nil {
		t.Fatal(err)
	}
	otherNonce, _ := service.NewCSRFNonce()
	preToken, _, _ := codec.Issue(service.PreLoginCSRFBinding(nonce))
	otherToken, _, _ := codec.Issue(service.PreLoginCSRFBinding(otherNonce))
	sessionToken, _, _ := codec.Issue(service.SessionCSRFBinding(principal.SessionID))

	type req struct {
		method, origin, referer, token, nonce, session string
	}
	tests := []struct {
		name   string
		r      req
		status int
	}{
		{"pre-login token from the right origin", req{"POST", testOrigin, "", preToken, nonce, ""}, 204},
		{"referer stands in for a missing origin", req{"POST", "", testOrigin + "/docs/", preToken, nonce, ""}, 204},
		{"neither origin nor referer", req{"POST", "", "", preToken, nonce, ""}, 403},
		{"other origin", req{"POST", "http://evil.example", "", preToken, nonce, ""}, 403},
		{"null origin", req{"POST", "null", "", preToken, nonce, ""}, 403},
		{"origin differs only by port", req{"POST", "http://localhost:8081", "", preToken, nonce, ""}, 403},
		{"referer of another site", req{"POST", "", "http://evil.example/page", preToken, nonce, ""}, 403},
		{"missing token", req{"POST", testOrigin, "", "", nonce, ""}, 403},
		{"token of another nonce", req{"POST", testOrigin, "", otherToken, nonce, ""}, 403},
		{"no nonce cookie", req{"POST", testOrigin, "", preToken, "", ""}, 403},
		{"malformed nonce cookie", req{"POST", testOrigin, "", preToken, "short", ""}, 403},
		{"signed in with the session token", req{"POST", testOrigin, "", sessionToken, "", "session-token"}, 204},
		{"signed in but sending the pre-login token", req{"POST", testOrigin, "", preToken, nonce, "session-token"}, 403},
		{"safe method needs no token", req{"GET", "", "", "", "", ""}, 204},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), tt.r.method, "/x", nil)
			if tt.r.origin != "" {
				r.Header.Set("Origin", tt.r.origin)
			}
			if tt.r.referer != "" {
				r.Header.Set("Referer", tt.r.referer)
			}
			if tt.r.token != "" {
				r.Header.Set(middleware.HeaderCSRFToken, tt.r.token)
			}
			if tt.r.nonce != "" {
				r.AddCookie(&nethttp.Cookie{Name: "csrf_nonce", Value: tt.r.nonce})
			}
			if tt.r.session != "" {
				r.AddCookie(&nethttp.Cookie{Name: "session", Value: tt.r.session})
			}
			resp, body := doRequest(t, app, r)
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d (%s), want %d", resp.StatusCode, body, tt.status)
			}
			if tt.status == 403 && decodeError(t, body).Code != "csrf_failed" {
				t.Errorf("code = %s, want csrf_failed", decodeError(t, body).Code)
			}
		})
	}
}

func TestRequireAuth(t *testing.T) {
	principal := domain.Principal{UserID: mustTestID(t), SessionID: mustTestID(t)}
	app := csrfTestApp(t, &fakeAuthService{principals: map[string]domain.Principal{"session-token": principal}}, newTestCSRF(t, newTestClock(time.Now())))

	for name, cookie := range map[string]string{"no cookie": "", "unknown session": "stale-token"} {
		r := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/private", nil)
		if cookie != "" {
			r.AddCookie(&nethttp.Cookie{Name: "session", Value: cookie})
		}
		resp, body := doRequest(t, app, r)
		if resp.StatusCode != nethttp.StatusUnauthorized || decodeError(t, body).Code != "unauthenticated" {
			t.Errorf("%s: got %d %s, want 401 unauthenticated", name, resp.StatusCode, body)
		}
	}

	r := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/private", nil)
	r.AddCookie(&nethttp.Cookie{Name: "session", Value: "session-token"})
	resp, body := doRequest(t, app, r)
	if resp.StatusCode != nethttp.StatusOK || string(body) != principal.UserID.String() {
		t.Errorf("signed in: got %d %s, want 200 with the user ID", resp.StatusCode, body)
	}
}

func TestRequestMeta(t *testing.T) {
	h := &authHandlers{clientIP: NewClientIPResolver(testTrustedProxies)}
	log, _ := newTestLogger()
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(log)})
	app.Use(middleware.RequestID())
	app.Get("/meta", func(c fiber.Ctx) error {
		meta, err := h.requestMeta(c)
		if err != nil {
			return err
		}
		return c.JSON(map[string]string{
			"request_id": meta.RequestID.String(), "client_ip": meta.ClientIP.String(),
			"masked_ip": meta.MaskedIP, "user_agent": meta.UserAgent,
		})
	})

	r := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/meta", nil)
	r.Header.Add("X-Forwarded-For", "6.6.6.6")
	r.Header.Add("X-Forwarded-For", "198.51.100.7")
	r.Header.Set("User-Agent", "agent\r\nX-Injected: yes")
	resp, body := doRequest(t, app, r)
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	if got["request_id"] != resp.Header.Get(middleware.HeaderRequestID) {
		t.Errorf("request_id = %q, want the X-Request-ID %q", got["request_id"], resp.Header.Get(middleware.HeaderRequestID))
	}
	if got["client_ip"] != "198.51.100.7" || got["masked_ip"] != "198.51.100.0/24" {
		t.Errorf("client IP = %q / %q, want the right-most untrusted hop and its /24 (if the peer address of app.Test is not in testTrustedProxies, add it there)", got["client_ip"], got["masked_ip"])
	}
	if strings.ContainsAny(got["user_agent"], "\r\n") {
		t.Errorf("user agent %q was not sanitized", got["user_agent"])
	}
}
