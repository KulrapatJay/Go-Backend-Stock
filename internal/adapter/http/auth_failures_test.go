package http

import (
	"errors"
	nethttp "net/http"
	"strings"
	"testing"
)

// failingTwin returns two harnesses over the same database: h is healthy, failing has an audit
// repository that always fails.
func failingTwin(t *testing.T) (h, failing *authHarness) {
	t.Helper()
	return newAuthHarness(t), newAuthHarness(t, withFailingAudit())
}

func requireAuditFailure(t *testing.T, what string, resp response, body []byte) {
	t.Helper()
	got := decodeError(t, body)
	if resp.StatusCode != nethttp.StatusInternalServerError || got.Code != "audit_write_failed" || got.Message != "Internal server error" {
		t.Fatalf("%s = %d %+v, want 500 audit_write_failed without details", what, resp.StatusCode, got)
	}
}

func TestRegisterRollsBackWithoutItsAudit(t *testing.T) {
	_, failing := failingTwin(t)
	email := uniqueEmail(t)
	resp, body := failing.newBrowser(t).register(email, "Zq9!kVx1")
	requireAuditFailure(t, "register", resp, body)
	if _, ok := failing.user(t, email); ok {
		t.Error("the account must be rolled back with its audit")
	}
	if failing.mailer.verificationCount(email) != 0 {
		t.Error("no code may be sent for a rolled-back registration")
	}
}

func TestConfirmRollsBackWithoutItsAudit(t *testing.T) {
	h, failing := failingTwin(t)
	email := uniqueEmail(t)
	h.newBrowser(t).register(email, "Zq9!kVx1")
	mail := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)

	resp, body := failing.newBrowser(t).confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	requireAuditFailure(t, "confirm", resp, body)
	if after, _ := h.user(t, email); after.Verified {
		t.Error("the email must stay unverified")
	}
	if ch := h.challenges(t, u.ID); !ch[0].Open || ch[0].FailedAttempts != 0 {
		t.Errorf("challenge = %+v, want it untouched", ch[0])
	}
}

func TestLoginWithoutItsAuditOpensNoSession(t *testing.T) {
	h, failing := failingTwin(t)
	email := uniqueEmail(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	b := failing.newBrowser(t)
	resp, body := b.login(email, "Zq9!kVx1")
	requireAuditFailure(t, "login", resp, body)
	if b.jar["session"] != "" || h.sessionsOf(t, u.ID) != 0 {
		t.Error("a login whose audit failed must not leave a session or a cookie")
	}

	resp, body = failing.newBrowser(t).login(email, "Wrong!Pass9")
	requireAuditFailure(t, "failed login", resp, body)
	if !strings.Contains(failing.logs.String(), `"event":"audit.write_failed"`) {
		t.Error("a lost login_failed audit must be logged")
	}
}

func TestMailerFailureDoesNotChangeTheAnswer(t *testing.T) {
	h := newAuthHarness(t)
	h.mailer.mu.Lock()
	h.mailer.err = errors.New("smtp is down for alice@example.com")
	h.mailer.mu.Unlock()
	email := uniqueEmail(t)

	resp, body := h.newBrowser(t).register(email, "Zq9!kVx1")
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register with a failing mailer = %d %s, want 202", resp.StatusCode, body)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, `"event":"auth.mail_not_sent"`) || !strings.Contains(logs, `"kind":"email_verification"`) {
		t.Errorf("logs %s should record auth.mail_not_sent with its kind", logs)
	}
	if strings.Contains(logs, email) || strings.Contains(logs, "alice@example.com") {
		t.Error("the mail failure log must not hold an address or the error text")
	}
}

func TestLogsHoldNoSecrets(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	var secrets []string
	collect := func() {
		if b.csrf != "" {
			secrets = append(secrets, b.csrf)
		}
		for _, v := range b.jar {
			secrets = append(secrets, v)
		}
	}

	b.fetchCSRF()
	collect()
	b.register(email, "Zq9!kVx1")
	collect()
	mail := h.mailer.lastVerification(t, email)
	b.confirm(mail.ChallengeID.String(), wrongCode(mail.OTP.Reveal()))
	b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	collect()
	h.newBrowser(t).login(email, "Wrong!Pass9")
	b.login(email, "Zq9!kVx1")
	collect()
	b.me()
	b.logout()
	collect()

	logs := h.logs.String()
	if logs == "" {
		t.Fatal("no logs were captured")
	}
	for _, s := range append(secrets, "Zq9!kVx1", "Wrong!Pass9", email, strings.Repeat("o", 32), strings.Repeat("c", 32), strings.Repeat("r", 32)) {
		if strings.Contains(logs, s) {
			t.Errorf("logs contain a secret value (%d characters)", len(s))
		}
	}
	for _, code := range []string{mail.OTP.Reveal(), wrongCode(mail.OTP.Reveal())} {
		if strings.Contains(logs, `"`+code+`"`) {
			t.Error("logs contain an OTP code")
		}
	}
}

func TestSessionStoreOutageIs503NotALogout(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")

	h.pool.Close() // the admin pool stays open
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/auth/csrf"} {
		resp, body := b.do(nethttp.MethodGet, path, nil, nil)
		if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "dependency_unavailable" {
			t.Errorf("GET %s with the store down = %d %s, want 503 dependency_unavailable", path, resp.StatusCode, body)
		}
	}
	if b.jar["session"] == "" {
		t.Error("an outage must not clear the session cookie")
	}
	if resp, _ := b.do(nethttp.MethodGet, "/readyz", nil, nil); resp.StatusCode != nethttp.StatusServiceUnavailable {
		t.Errorf("readyz = %d, want 503", resp.StatusCode)
	}
}
