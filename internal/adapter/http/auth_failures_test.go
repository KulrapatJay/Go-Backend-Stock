package http

import (
	"context"
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

// A logout whose audit cannot be written rolls the revoke back, so the session is still valid: the
// browser must keep its cookie, or it would look signed out while the session lives on for days.
func TestLogoutWithoutItsAuditKeepsTheSession(t *testing.T) {
	h, failing := failingTwin(t)
	email := uniqueEmail(t)
	healthy := h.newBrowser(t)
	healthy.signUpAndVerify(email, "Zq9!kVx1")
	if resp, body := healthy.login(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	u, _ := h.user(t, email)

	// Both harnesses share the database, so the failing twin accepts the healthy twin's session. Its
	// CSRF token must be bound to that session, so it fetches its own.
	b := failing.newBrowser(t)
	b.jar["session"] = healthy.jar["session"]
	resp, body := b.logout()
	requireAuditFailure(t, "logout", resp, body)
	if b.jar["session"] == "" {
		t.Error("a logout that failed must not clear the session cookie")
	}
	for _, c := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "session=") {
			t.Errorf("a failed logout must not touch the session cookie, got Set-Cookie %q", c)
		}
	}
	var live int
	if err := h.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM app.sessions WHERE user_id = $1 AND revoked_at IS NULL`, u.ID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Errorf("%d live sessions after the failed logout, want the 1 session still valid", live)
	}
	if resp, _ := healthy.me(); resp.StatusCode != nethttp.StatusOK {
		t.Errorf("me after the failed logout = %d, want 200", resp.StatusCode)
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

// secretJar remembers every secret value the browsers held: CSRF tokens and cookie values.
type secretJar struct{ values []string }

// collect records what b holds now. Call it after every request: a cookie is gone from the jar once
// the server clears it, and a CSRF token is replaced by the next fetch.
func (j *secretJar) collect(browsers ...*browser) {
	for _, b := range browsers {
		if b.csrf != "" {
			j.values = append(j.values, b.csrf)
		}
		for _, v := range b.jar {
			j.values = append(j.values, v)
		}
	}
}

// requireNoSecrets fails when logs hold any of the collected secrets, the plain values (passwords
// and email addresses) the test used, or the keys the harness is configured with. An OTP is a short digit string, so it is only
// searched as a whole JSON string value.
func requireNoSecrets(t *testing.T, logs string, jar *secretJar, plain, otps []string) {
	t.Helper()
	if logs == "" {
		t.Fatal("no logs were captured")
	}
	secrets := append([]string{}, jar.values...)
	secrets = append(secrets, plain...)
	secrets = append(secrets, strings.Repeat("o", 32), strings.Repeat("c", 32), strings.Repeat("r", 32))
	for _, s := range secrets {
		if strings.Contains(logs, s) {
			t.Errorf("logs contain a secret value (%d characters)", len(s))
		}
	}
	for _, code := range otps {
		if strings.Contains(logs, `"`+code+`"`) {
			t.Error("logs contain an OTP code")
		}
	}
}

// TestAccessLogHoldsNoSecrets covers the access-log lines of the happy-path flows (register, confirm,
// login, me, logout, with a wrong code and a wrong password): those flows log nothing else. The
// error-path loggers are covered by TestErrorLogsHoldNoSecrets and the other tests in this file.
func TestAccessLogHoldsNoSecrets(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	other := h.newBrowser(t)
	email := uniqueEmail(t)
	jar := &secretJar{}

	b.fetchCSRF()
	jar.collect(b)
	b.register(email, "Zq9!kVx1")
	jar.collect(b)
	mail := h.mailer.lastVerification(t, email)
	b.confirm(mail.ChallengeID.String(), wrongCode(mail.OTP.Reveal()))
	jar.collect(b)
	b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	jar.collect(b)
	other.login(email, "Wrong!Pass9")
	jar.collect(b, other)
	b.login(email, "Zq9!kVx1")
	jar.collect(b, other)
	b.me()
	b.fetchCSRF() // bound to the session now
	jar.collect(b)
	b.logout()
	jar.collect(b)

	logs := h.logs.String()
	if got := strings.Count(logs, `"msg":"http request"`); got < 10 {
		t.Fatalf("only %d access-log lines were captured; the scan would be vacuous", got)
	}
	requireNoSecrets(t, logs, jar, []string{email, "Zq9!kVx1", "Wrong!Pass9"}, []string{mail.OTP.Reveal(), wrongCode(mail.OTP.Reveal())})
}

// TestErrorLogsHoldNoSecrets drives the error-path loggers: a lost audit write (register and login)
// and a mail that could not be sent.
func TestErrorLogsHoldNoSecrets(t *testing.T) {
	h, failing := failingTwin(t)
	jar := &secretJar{}
	email := uniqueEmail(t)

	// audit.write_failed: a register and a login whose audit cannot be written.
	fb := failing.newBrowser(t)
	fb.register(email, "Zq9!kVx1")
	jar.collect(fb)
	signedUp := uniqueEmail(t) // an account made through the healthy twin, to log in to
	h.newBrowser(t).signUpAndVerify(signedUp, "Zq9!kVx1")
	fb.login(signedUp, "Zq9!kVx1")
	jar.collect(fb)
	fb.login(signedUp, "Wrong!Pass9")
	jar.collect(fb)
	failLogs := failing.logs.String()
	if !strings.Contains(failLogs, `"event":"audit.write_failed"`) {
		t.Fatal("the failing-audit logs should hold audit.write_failed; the scan would be vacuous")
	}
	requireNoSecrets(t, failLogs, jar, []string{email, signedUp, "Zq9!kVx1", "Wrong!Pass9"}, nil)

	// auth.mail_not_sent: the mailer is down.
	mh := newAuthHarness(t)
	mh.mailer.mu.Lock()
	mh.mailer.err = errors.New("smtp is down")
	mh.mailer.mu.Unlock()
	mjar := &secretJar{}
	mailEmail := uniqueEmail(t)
	mb := mh.newBrowser(t)
	mb.register(mailEmail, "Zq9!kVx1")
	mjar.collect(mb)
	mb.requestCode(mailEmail)
	mjar.collect(mb)
	mail := mh.mailer.lastVerification(t, mailEmail)
	mailLogs := mh.logs.String()
	if !strings.Contains(mailLogs, `"event":"auth.mail_not_sent"`) {
		t.Fatal("the mail-failure logs should hold auth.mail_not_sent; the scan would be vacuous")
	}
	requireNoSecrets(t, mailLogs, mjar, []string{mailEmail, "Zq9!kVx1"}, []string{mail.OTP.Reveal()})
}

// With the session store down, the session middleware (Authenticate) already fails with 503 before
// the logout handler runs, so neither the handler nor the use case touches the cookie or the session.
func TestSessionStoreOutageIs503NotALogout(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")
	b.fetchCSRF() // a token bound to the session, held through the outage
	u, _ := h.user(t, email)

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
	resp, body := b.do(nethttp.MethodPost, "/api/v1/auth/logout", nil, nil)
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "dependency_unavailable" {
		t.Errorf("logout with the store down = %d %s, want 503 dependency_unavailable", resp.StatusCode, body)
	}
	if b.jar["session"] == "" {
		t.Error("a failed logout must not clear the session cookie")
	}
	var revoked int
	if err := h.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM app.sessions WHERE user_id = $1 AND revoked_at IS NULL`, u.ID).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Errorf("%d live sessions after the outage, want the 1 session untouched", revoked)
	}
	if resp, _ := b.do(nethttp.MethodGet, "/readyz", nil, nil); resp.StatusCode != nethttp.StatusServiceUnavailable {
		t.Errorf("readyz = %d, want 503", resp.StatusCode)
	}
}
