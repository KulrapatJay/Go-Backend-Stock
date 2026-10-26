package http

import (
	"encoding/json"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

func decodeUser(t *testing.T, body []byte) UserResponse {
	t.Helper()
	var u UserResponse
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	return u
}

func TestLoginMeLogoutFlow(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	resp, body := b.login(email, "Zq9!kVx1")
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	var out SessionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.User.ID != u.ID || out.User.Email != email || !out.User.EmailVerified || out.ExpiresAt.Location() != time.UTC {
		t.Errorf("login body = %+v", out)
	}
	if strings.Contains(string(body), "argon2") || strings.Contains(string(body), "password") {
		t.Errorf("login body %s must not carry the password or its hash", body)
	}
	if wantExpiry := h.clock.Now().Add(7 * 24 * time.Hour); !out.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("expires_at = %v, want %v", out.ExpiresAt, wantExpiry)
	}
	raw := strings.ToLower(strings.Join(resp.Header.Values("Set-Cookie"), "\n"))
	for _, want := range []string{"session=", "httponly", "samesite=lax", "path=/"} {
		if !strings.Contains(raw, want) {
			t.Errorf("Set-Cookie %q lacks %q", raw, want)
		}
	}
	if b.jar["session"] == "" || b.jar["csrf_nonce"] != "" {
		t.Errorf("cookies after login = %v, want a session and no nonce", b.jar)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.login_succeeded" || rows[0].Actor != u.ID ||
		rows[0].SessionID == "" || rows[0].Metadata["method"] != "password" {
		t.Fatalf("login audit = %+v", rows)
	}
	sessionID := rows[0].SessionID

	resp, body = b.me()
	if resp.StatusCode != nethttp.StatusOK || decodeUser(t, body).ID != u.ID {
		t.Fatalf("me = %d %s", resp.StatusCode, body)
	}

	resp, body = b.logout()
	if resp.StatusCode != nethttp.StatusNoContent || b.jar["session"] != "" {
		t.Fatalf("logout = %d %s, cookies %v; want 204 and the session cookie cleared", resp.StatusCode, body, b.jar)
	}
	rows = h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.logout" || rows[0].Actor != u.ID || rows[0].SessionID != sessionID || rows[0].TargetID != sessionID {
		t.Errorf("logout audit = %+v", rows)
	}
	var reason string
	if err := h.admin.QueryRow(t.Context(), `SELECT COALESCE(revoke_reason, '') FROM app.sessions WHERE id = $1`, sessionID).Scan(&reason); err != nil || reason != "logout" {
		t.Errorf("revoke_reason = %q (%v), want logout", reason, err)
	}
	if resp, _ := b.me(); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("me after logout = %d, want 401", resp.StatusCode)
	}
}

func TestLogoutOfARevokedSessionIsNotAuditedAgain(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")
	stolen := b.jar["session"]
	b.logout()

	b.jar["session"] = stolen // a copy of the cookie made before logout
	if resp, _ := b.me(); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Fatalf("me with the revoked cookie = %d, want 401", resp.StatusCode)
	}
	resp, _ := b.logout()
	if resp.StatusCode != nethttp.StatusNoContent || b.jar["session"] != "" {
		t.Errorf("second logout = %d, want 204 and the cookie cleared", resp.StatusCode)
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("a second logout must not be audited, got %v", eventTypes(rows))
	}
}

func TestSessionExpiresAfterItsLifetime(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")

	h.clock.Advance(7*24*time.Hour - time.Second)
	if resp, _ := b.me(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("me one second before expiry = %d, want 200", resp.StatusCode)
	}
	h.clock.Advance(time.Second)
	resp, body := b.me()
	if resp.StatusCode != nethttp.StatusUnauthorized || decodeError(t, body).Code != "unauthenticated" {
		t.Errorf("me at expires_at = %d %s, want 401 unauthenticated", resp.StatusCode, body)
	}
}

// The browser still holds the expired cookie and a CSRF token bound to the old session.
func TestLogoutAfterTheSessionExpired(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")
	b.fetchCSRF() // bound to the session
	h.clock.Advance(7 * 24 * time.Hour)

	resp, body := b.do(nethttp.MethodPost, "/api/v1/auth/logout", nil, nil)
	if resp.StatusCode != nethttp.StatusForbidden || decodeError(t, body).Code != "csrf_failed" {
		t.Fatalf("logout with the old token = %d %s, want 403 csrf_failed (the binding is gone)", resp.StatusCode, body)
	}
	resp, body = b.logout() // a fresh token, now bound to a nonce
	if resp.StatusCode != nethttp.StatusNoContent || b.jar["session"] != "" {
		t.Errorf("logout after expiry = %d %s, want 204 and the cookie cleared", resp.StatusCode, body)
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("an expired session is not logged out again, got %v", eventTypes(rows))
	}
}

func TestLoginFailuresLookTheSame(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	known := uniqueEmail(t)
	b.signUpAndVerify(known, "Zq9!kVx1")
	googleOnly := uniqueEmail(t)
	gEmail, err := domain.ParseEmail(googleOnly)
	if err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	if err := h.store.Users().Insert(t.Context(), domain.User{ID: mustTestID(t), Email: gEmail, EmailVerifiedAt: &now, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	cases := map[string][2]string{
		"unknown email":  {uniqueEmail(t), "Zq9!kVx1"},
		"wrong password": {known, "Wrong!Pass9"},
		"google-only":    {googleOnly, "Zq9!kVx1"},
		"too long":       {known, "Zq9!kVx1" + strings.Repeat("a", 30)},
	}
	bodies := map[string]string{}
	for name, c := range cases {
		resp, body := h.newBrowser(t).login(c[0], c[1])
		if resp.StatusCode != nethttp.StatusUnauthorized || decodeError(t, body).Code != "invalid_credentials" {
			t.Fatalf("%s: %d %s, want 401 invalid_credentials", name, resp.StatusCode, body)
		}
		if len(resp.Header.Values("Set-Cookie")) != 0 {
			t.Errorf("%s: a failed login must not set cookies", name)
		}
		bodies[name] = stripJSONField(t, body, "error", "request_id")
		rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
		if len(rows) != 1 || rows[0].EventType != "auth.login_failed" || rows[0].Actor != "" ||
			rows[0].Reason != "invalid_credentials" || rows[0].Metadata["method"] != "password" {
			t.Errorf("%s: audit = %+v", name, rows)
		}
	}
	for name, body := range bodies {
		if body != bodies["unknown email"] {
			t.Errorf("%s body %s differs from %s", name, body, bodies["unknown email"])
		}
	}
}

func TestLoginBeforeVerifyingIs403(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	resp, body := b.login(email, "Zq9!kVx1")
	if resp.StatusCode != nethttp.StatusForbidden || decodeError(t, body).Code != "email_not_verified" {
		t.Fatalf("login before verifying = %d %s, want 403 email_not_verified", resp.StatusCode, body)
	}
	if b.jar["session"] != "" || h.sessionsOf(t, u.ID) != 0 {
		t.Error("an unverified login must not create a session")
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].Reason != "email_not_verified" || rows[0].Actor != "" {
		t.Errorf("audit = %+v, want login_failed email_not_verified with actor NULL", rows)
	}
	// A wrong password for the same account still says 401: only the right password learns 403.
	if resp, _ := b.login(email, "Wrong!Pass9"); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("wrong password of an unverified account = %d, want 401", resp.StatusCode)
	}
}

func TestLoginIgnoresEmailCase(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	mixed := "Mixed-" + mustTestID(t).String() + "@Example.COM"
	b.signUpAndVerify(mixed, "Zq9!kVx1")
	if resp, body := b.login(strings.ToLower(mixed), "Zq9!kVx1"); resp.StatusCode != nethttp.StatusOK {
		t.Errorf("login with the lower-case email = %d %s", resp.StatusCode, body)
	}
}

func TestLoginUpgradesAnOldHashAndKeepsWorking(t *testing.T) {
	old := newAuthHarness(t) // hashes with m=64
	email := uniqueEmail(t)
	old.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	before, _ := old.user(t, email)

	h := newAuthHarness(t, withHasherParams(service.Argon2Params{MemoryKiB: 128, Iterations: 1, Parallelism: 1}))
	if resp, body := h.newBrowser(t).login(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("first login = %d %s", resp.StatusCode, body)
	}
	after, _ := h.user(t, email)
	if after.PasswordHash == before.PasswordHash || !strings.Contains(after.PasswordHash, "m=128,t=1,p=1") {
		t.Fatalf("hash after login = %s, want it upgraded to m=128", after.PasswordHash)
	}
	if resp, _ := h.newBrowser(t).login(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusOK {
		t.Error("the upgraded hash must still accept the password")
	}
	if again, _ := h.user(t, email); again.PasswordHash != after.PasswordHash {
		t.Error("a hash with the current parameters must not be rehashed")
	}
}

func TestLoginIsRateLimitedPerEmail(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	for i := 0; i < 10; i++ {
		if resp, _ := h.newBrowser(t).login(email, "Wrong!Pass9"); resp.StatusCode != nethttp.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, resp.StatusCode)
		}
	}
	resp, body := h.newBrowser(t).login(email, "Wrong!Pass9")
	if resp.StatusCode != nethttp.StatusTooManyRequests || decodeError(t, body).Code != "rate_limited" {
		t.Fatalf("attempt 11 = %d %s, want 429", resp.StatusCode, body)
	}
	if wait, err := strconv.Atoi(resp.Header.Get("Retry-After")); err != nil || wait < 1 || wait > 900 {
		t.Errorf("Retry-After = %q, want 1..900", resp.Header.Get("Retry-After"))
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if !slices.Equal(eventTypes(rows), []string{"security.rate_limited"}) || rows[0].Metadata["scope"] != "login_email" {
		t.Errorf("audit = %+v, want security.rate_limited for login_email", rows)
	}
}

func TestMeWithoutASessionIs401(t *testing.T) {
	h := newAuthHarness(t)
	resp, body := h.newBrowser(t).me()
	if resp.StatusCode != nethttp.StatusUnauthorized || decodeError(t, body).Code != "unauthenticated" {
		t.Errorf("me = %d %s, want 401 unauthenticated", resp.StatusCode, body)
	}
}
