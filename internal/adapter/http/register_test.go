package http

import (
	"encoding/json"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func decodeAccepted(t *testing.T, body []byte) OTPAcceptedResponse {
	t.Helper()
	var out OTPAcceptedResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	if _, err := domain.ParseID("challenge_id", out.ChallengeID); err != nil || out.ExpiresIn != 600 || out.Message == "" {
		t.Fatalf("202 body = %+v, want a UUID challenge_id, expires_in 600 and a message", out)
	}
	return out
}

func TestRegisterNewEmailCreatesAnUnverifiedAccountAndSendsACode(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)

	resp, body := b.register(email, "Zq9!kVx1")
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register = %d %s, want 202", resp.StatusCode, body)
	}
	out := decodeAccepted(t, body)

	u, ok := h.user(t, email)
	if !ok || u.Verified || !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatalf("user = %+v (found %v), want an unverified account with an Argon2id hash", u, ok)
	}
	ch := h.challenges(t, u.ID)
	if len(ch) != 1 || ch[0].ID != out.ChallengeID || ch[0].Purpose != "email_verification" || !ch[0].Open {
		t.Errorf("challenges = %+v, want the one open email_verification challenge from the response", ch)
	}
	mail := h.mailer.lastVerification(t, email)
	if mail.ChallengeID.String() != out.ChallengeID || len(mail.OTP.Reveal()) != 6 {
		t.Errorf("mail = %v, want the response's challenge and a 6-digit code", mail)
	}

	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if got := eventTypes(rows); !slices.Equal(got, []string{"auth.registered", "auth.email_verification_requested"}) {
		t.Fatalf("audit = %v", got)
	}
	for _, r := range rows {
		if r.Actor != "" || r.Outcome != "success" || r.TargetID != u.ID {
			t.Errorf("audit row %+v: want actor NULL (not verified yet), success, target the user", r)
		}
		if ip, _ := r.Metadata["ip"].(string); !strings.HasSuffix(ip, "::/48") || r.Metadata["user_agent"] != "integration-test/1.0" {
			t.Errorf("metadata %v: want the /48 of the client IP and the user agent", r.Metadata)
		}
		if strings.Contains(string(mustMarshal(t, r.Metadata)), email) {
			t.Errorf("metadata %v holds the raw email", r.Metadata)
		}
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRegisterExistingEmailLooksTheSameAndChangesNothing(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)

	first, firstBody := b.register(email, "Zq9!kVx1")
	if first.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("first register = %d", first.StatusCode)
	}
	before, _ := h.user(t, email)
	h.clock.Advance(61 * time.Second) // past the per-email cooldown

	second, secondBody := b.register(email, "Other!Pass9")
	if second.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("second register = %d %s, want 202 like a new email", second.StatusCode, secondBody)
	}
	if stripJSONField(t, firstBody, "challenge_id") != stripJSONField(t, secondBody, "challenge_id") {
		t.Errorf("bodies differ beyond the challenge ID:\n%s\n%s", firstBody, secondBody)
	}
	if decodeAccepted(t, firstBody).ChallengeID == decodeAccepted(t, secondBody).ChallengeID {
		t.Error("the decoy challenge ID must be fresh, not the real one")
	}
	after, _ := h.user(t, email)
	if after.PasswordHash != before.PasswordHash || len(h.challenges(t, before.ID)) != 1 {
		t.Error("registering an existing email must not change the account or add a challenge")
	}
	if h.mailer.attemptCount(email) != 1 || h.mailer.verificationCount(email) != 1 {
		t.Errorf("mails: %d attempt notices and %d codes, want 1 and 1", h.mailer.attemptCount(email), h.mailer.verificationCount(email))
	}
	if rows := h.auditFor(t, second.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("an existing email must not write audit, got %v", eventTypes(rows))
	}
}

func TestRegisterTreatsEmailCaseAsTheSameAccount(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	mixed := "Case-" + mustTestID(t).String() + "@Example.COM"
	lower := strings.ToLower(mixed)

	if resp, body := b.register(mixed, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register = %d %s", resp.StatusCode, body)
	}
	if _, ok := h.user(t, lower); !ok {
		t.Fatal("the account must be stored under the lower-cased email")
	}
	h.clock.Advance(61 * time.Second)
	if resp, _ := b.register(lower, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("second register = %d", resp.StatusCode)
	}
	if h.mailer.attemptCount(lower) != 1 {
		t.Error("the lower-case spelling must reach the existing account, not create a second one")
	}
}

func TestRegisterSameEmailConcurrentlyCreatesOneAccount(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	browsers := []*browser{h.newBrowser(t), h.newBrowser(t), h.newBrowser(t)}
	for _, b := range browsers {
		b.fetchCSRF()
	}

	var wg sync.WaitGroup
	statuses := make([]int, len(browsers))
	errs := make([]error, len(browsers))
	for i, b := range browsers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _, err := b.tryDo(nethttp.MethodPost, "/api/v1/auth/register", map[string]string{"email": email, "password": "Zq9!kVx1"}, nil)
			statuses[i], errs[i] = resp.StatusCode, err
		}()
	}
	wg.Wait()

	accepted := 0
	for i, s := range statuses {
		if errs[i] != nil {
			t.Fatalf("request %d: %v", i, errs[i])
		}
		switch s {
		case nethttp.StatusAccepted:
			accepted++
		case nethttp.StatusTooManyRequests: // the per-email cooldown serializes the attempts
		default:
			t.Errorf("request %d = %d, want 202 or 429", i, s)
		}
	}
	if accepted == 0 {
		t.Error("no registration was accepted")
	}
	var n int
	if err := h.admin.QueryRow(t.Context(), `SELECT count(*) FROM app.users WHERE email = $1`, email).Scan(&n); err != nil || n != 1 {
		t.Errorf("accounts for the email = %d (%v), want 1", n, err)
	}
}

func TestRegisterRejectsAWeakPasswordWithEveryReason(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)

	resp, body := b.register(email, "abc")
	got := decodeError(t, body)
	if resp.StatusCode != nethttp.StatusBadRequest || got.Code != "invalid_input" ||
		!slices.Equal(got.Details, []string{"password_too_short", "password_missing_uppercase", "password_missing_special"}) {
		t.Errorf("weak password = %d %+v", resp.StatusCode, got)
	}

	_, body = b.register(email, "P@ssw0rd")
	if got := decodeError(t, body); !slices.Equal(got.Details, []string{"password_too_common"}) {
		t.Errorf("common password details = %v, want [password_too_common]", got.Details)
	}
	if _, ok := h.user(t, email); ok || h.mailer.verificationCount(email) != 0 {
		t.Error("a refused password must create nothing and send nothing")
	}
}

func TestRegisterNeedsCSRFAndOrigin(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.fetchCSRF()
	payload := map[string]string{"email": email, "password": "Zq9!kVx1"}

	for name, extra := range map[string]map[string]string{
		"no token":     {"X-CSRF-Token": ""},
		"other origin": {"Origin": "http://evil.example"},
		"no origin":    {"Origin": ""},
	} {
		resp, body := b.do(nethttp.MethodPost, "/api/v1/auth/register", payload, extra)
		if resp.StatusCode != nethttp.StatusForbidden || decodeError(t, body).Code != "csrf_failed" {
			t.Errorf("%s: %d %s, want 403 csrf_failed", name, resp.StatusCode, body)
		}
	}
	if _, ok := h.user(t, email); ok {
		t.Error("a request refused by CSRF must not create an account")
	}
}

func TestRegisterRejectsMalformedJSON(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	b.fetchCSRF()
	for name, raw := range map[string]string{
		"unknown field": `{"email":"a@example.com","password":"Zq9!kVx1","admin":true}`,
		"trailing data": `{"email":"a@example.com","password":"Zq9!kVx1"} {}`,
		"wrong type":    `{"email":5,"password":"Zq9!kVx1"}`,
		"not json":      `email=a@example.com`,
	} {
		resp, body := b.do(nethttp.MethodPost, "/api/v1/auth/register", raw, nil)
		if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "malformed_json" {
			t.Errorf("%s: %d %s, want 400 malformed_json", name, resp.StatusCode, body)
		}
	}
}

func TestRegisterIsRateLimitedPerEmailAndAuditsTheScope(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	if resp, _ := b.register(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("first register = %d", resp.StatusCode)
	}

	resp, body := b.register(email, "Zq9!kVx1")
	if resp.StatusCode != nethttp.StatusTooManyRequests || decodeError(t, body).Code != "rate_limited" {
		t.Fatalf("second register within the cooldown = %d %s, want 429", resp.StatusCode, body)
	}
	wait, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || wait < 1 || wait > 60 {
		t.Errorf("Retry-After = %q, want 1..60 seconds", resp.Header.Get("Retry-After"))
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "security.rate_limited" || rows[0].Outcome != "denied" ||
		rows[0].Metadata["scope"] != "email_verification_request_email" || rows[0].Actor != "" {
		t.Errorf("audit = %+v, want one security.rate_limited denial naming the scope", rows)
	}

	again, _ := b.register(email, "Zq9!kVx1")
	if again.StatusCode != nethttp.StatusTooManyRequests {
		t.Fatalf("third register = %d, want 429", again.StatusCode)
	}
	if rows := h.auditFor(t, again.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("a key is audited at most once a minute, got %v", eventTypes(rows))
	}
}

func TestRegisterTakesAtLeastTheResponseFloor(t *testing.T) {
	h := newAuthHarness(t, withResponseFloor(300*time.Millisecond))
	b := h.newBrowser(t)
	b.fetchCSRF()
	start := time.Now()
	resp, _ := b.do(nethttp.MethodPost, "/api/v1/auth/register", map[string]string{"email": uniqueEmail(t), "password": "Zq9!kVx1"}, nil)
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register = %d", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Errorf("register answered after %v, want at least the 300ms floor", elapsed)
	}
}
