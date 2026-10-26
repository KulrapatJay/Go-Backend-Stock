package http

import (
	"crypto/rand"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func failureReasons(rows []auditRow) []string {
	var out []string
	for _, r := range rows {
		if r.EventType == "auth.email_verification_failed" {
			out = append(out, r.Reason)
		}
	}
	return out
}

func TestConfirmVerifiesTheEmailOnce(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	if resp, _ := b.register(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatal("register failed")
	}
	mail := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)

	resp, body := b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	if resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("confirm = %d %s, want 204", resp.StatusCode, body)
	}
	if after, _ := h.user(t, email); !after.Verified {
		t.Error("the email must be verified")
	}
	if ch := h.challenges(t, u.ID); ch[0].Open {
		t.Error("the challenge must be consumed")
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.email_verified" || rows[0].Actor != u.ID || rows[0].Outcome != "success" {
		t.Errorf("audit = %+v, want auth.email_verified by the user", rows)
	}
	if b.jar["session"] != "" {
		t.Error("confirming must not sign the user in")
	}

	again, body := b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	if again.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_verification" {
		t.Errorf("second confirm = %d %s, want 400 invalid_verification", again.StatusCode, body)
	}
	if got := failureReasons(h.auditFor(t, again.Header.Get("X-Request-ID"))); len(got) != 1 || got[0] != "challenge_closed" {
		t.Errorf("failure reasons = %v, want [challenge_closed]", got)
	}
}

func TestConfirmWrongCodeCountsAndAuditsEveryAttempt(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	mail := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)
	wrong := wrongCode(mail.OTP.Reveal())

	for i := 1; i <= 5; i++ {
		resp, body := b.confirm(mail.ChallengeID.String(), wrong)
		if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_verification" {
			t.Fatalf("wrong attempt %d = %d %s", i, resp.StatusCode, body)
		}
		if got := h.challenges(t, u.ID)[0].FailedAttempts; got != i {
			t.Fatalf("failed_attempts after %d wrong codes = %d", i, got)
		}
	}
	resp, _ := b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	if resp.StatusCode != nethttp.StatusBadRequest {
		t.Errorf("the right code after 5 wrong ones = %d, want 400 (the challenge is exhausted)", resp.StatusCode)
	}
	reasons := failureReasons(h.auditByTarget(t, mail.ChallengeID.String()))
	if len(reasons) != 6 || reasons[0] != "otp_mismatch" || reasons[5] != "too_many_attempts" {
		t.Errorf("failure reasons = %v, want 5 x otp_mismatch then too_many_attempts", reasons)
	}
	for _, r := range h.auditByTarget(t, mail.ChallengeID.String()) {
		if r.Actor != "" {
			t.Errorf("a failed verification must have actor NULL, got %s", r.Actor)
		}
	}
	if after, _ := h.user(t, email); after.Verified {
		t.Error("the email must stay unverified")
	}
}

func TestConfirmExpiredCodeIsRefused(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	mail := h.mailer.lastVerification(t, email)

	h.clock.Advance(10 * time.Minute) // exactly at expires_at the code is over
	resp, _ := b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	if resp.StatusCode != nethttp.StatusBadRequest {
		t.Fatalf("expired code = %d, want 400", resp.StatusCode)
	}
	if got := failureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); len(got) != 1 || got[0] != "challenge_expired" {
		t.Errorf("failure reasons = %v, want [challenge_expired]", got)
	}
}

func TestConfirmUnknownOrPasswordResetChallengeIsRefused(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	// A password-reset challenge of the same user: its ID must never work for email verification.
	userID, err := domain.ParseID("user_id", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	resetID := mustTestID(t)
	hash := make([]byte, domain.HashSize)
	if _, err := rand.Read(hash); err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	reset, err := domain.NewOTPChallenge(domain.OTPChallenge{ID: resetID, UserID: userID, Purpose: domain.OTPPurposePasswordReset,
		OTPHash: hash, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.OTPChallenges().Insert(t.Context(), reset); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]string{"unknown": mustTestID(t).String(), "password reset": resetID.String()} {
		resp, body := b.confirm(id, "123456")
		if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_verification" {
			t.Errorf("%s: %d %s, want 400 invalid_verification", name, resp.StatusCode, body)
		}
		if got := failureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); len(got) != 1 || got[0] != "challenge_not_found" {
			t.Errorf("%s: failure reasons = %v, want [challenge_not_found]", name, got)
		}
	}
}

func TestConfirmRejectsNonASCIIDigitsWithoutCountingAnAttempt(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	mail := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)

	for _, otp := range []string{"๑๒๓๔๕๖", " 12345", "12345", "1234567"} {
		resp, body := b.confirm(mail.ChallengeID.String(), otp)
		got := decodeError(t, body)
		if resp.StatusCode != nethttp.StatusBadRequest || got.Code != "invalid_input" || !strings.HasPrefix(got.Message, "otp") {
			t.Errorf("otp %q = %d %+v, want 400 invalid_input about otp", otp, resp.StatusCode, got)
		}
	}
	if got := h.challenges(t, u.ID)[0].FailedAttempts; got != 0 {
		t.Errorf("failed_attempts = %d, want 0: a code that is not 6 ASCII digits is not an attempt", got)
	}
	resp, body := b.confirm("not-a-uuid", "123456")
	if resp.StatusCode != nethttp.StatusBadRequest || !strings.HasPrefix(decodeError(t, body).Message, "challenge_id") {
		t.Errorf("bad challenge_id = %d %s", resp.StatusCode, body)
	}
}

// Concurrent wrong codes must all be counted: Lock takes the challenge row FOR UPDATE (review of 3b).
func TestConcurrentWrongCodesAreAllCounted(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	owner := h.newBrowser(t)
	owner.register(email, "Zq9!kVx1")
	mail := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)
	wrong := wrongCode(mail.OTP.Reveal())

	browsers := make([]*browser, 5)
	for i := range browsers {
		browsers[i] = h.newBrowser(t)
		browsers[i].fetchCSRF()
	}
	var wg sync.WaitGroup
	statuses := make([]int, len(browsers))
	for i, b := range browsers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _, err := b.tryDo(nethttp.MethodPost, "/api/v1/auth/email-verification/confirm",
				map[string]string{"challenge_id": mail.ChallengeID.String(), "otp": wrong}, nil)
			if err == nil {
				statuses[i] = resp.StatusCode
			}
		}()
	}
	wg.Wait()
	for i, s := range statuses {
		if s != nethttp.StatusBadRequest {
			t.Errorf("request %d = %d, want 400", i, s)
		}
	}
	if got := h.challenges(t, u.ID)[0].FailedAttempts; got != 5 {
		t.Errorf("failed_attempts = %d, want 5", got)
	}
	if got := failureReasons(h.auditByTarget(t, mail.ChallengeID.String())); len(got) != 5 {
		t.Errorf("failure audits = %d, want 5", len(got))
	}
}

func TestRequestSendsANewCodeAndInvalidatesTheOld(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	old := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)
	h.clock.Advance(61 * time.Second)

	resp, body := b.requestCode(email)
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("request = %d %s", resp.StatusCode, body)
	}
	out := decodeAccepted(t, body)
	fresh := h.mailer.lastVerification(t, email)
	if fresh.ChallengeID.String() != out.ChallengeID || fresh.ChallengeID == old.ChallengeID {
		t.Fatalf("the new email must carry the new challenge from the response")
	}
	ch := h.challenges(t, u.ID)
	if len(ch) != 2 || ch[0].Open || !ch[1].Open {
		t.Errorf("challenges = %+v, want the old one closed and the new one open", ch)
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 1 || rows[0].EventType != "auth.email_verification_requested" {
		t.Errorf("audit = %v", eventTypes(rows))
	}
	if resp, _ := b.confirm(old.ChallengeID.String(), old.OTP.Reveal()); resp.StatusCode != nethttp.StatusBadRequest {
		t.Error("the old code must stop working")
	}
	if resp, _ := b.confirm(fresh.ChallengeID.String(), fresh.OTP.Reveal()); resp.StatusCode != nethttp.StatusNoContent {
		t.Error("the new code must work")
	}
}

func TestRequestAnswersTheSameForEveryOtherEmail(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)

	verified := uniqueEmail(t)
	b.signUpAndVerify(verified, "Zq9!kVx1")
	googleOnly := uniqueEmail(t)
	gEmail, err := domain.ParseEmail(googleOnly)
	if err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	if err := h.store.Users().Insert(t.Context(), domain.User{ID: mustTestID(t), Email: gEmail, EmailVerifiedAt: &now, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	unverified := uniqueEmail(t)
	b.register(unverified, "Zq9!kVx1")
	h.clock.Advance(61 * time.Second)

	bodies := map[string]string{}
	for name, email := range map[string]string{"unknown": uniqueEmail(t), "verified": verified, "google-only": googleOnly, "unverified": unverified} {
		before := h.mailer.verificationCount(email)
		resp, body := b.requestCode(email)
		if resp.StatusCode != nethttp.StatusAccepted {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, body)
		}
		decodeAccepted(t, body)
		bodies[name] = stripJSONField(t, body, "challenge_id")
		sent := h.mailer.verificationCount(email) - before
		if wantSent := map[bool]int{true: 1, false: 0}[name == "unverified"]; sent != wantSent {
			t.Errorf("%s: %d codes sent, want %d", name, sent, wantSent)
		}
	}
	for name, body := range bodies {
		if body != bodies["unknown"] {
			t.Errorf("%s body %s differs from the unknown-email body %s", name, body, bodies["unknown"])
		}
	}
}

func TestRequestShareTheRegisterCooldown(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	resp, body := b.requestCode(email)
	if resp.StatusCode != nethttp.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("request right after register = %d %s, want 429 with Retry-After", resp.StatusCode, body)
	}
}
