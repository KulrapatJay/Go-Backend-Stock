package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	nethttp "net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

// resetCode asks b for a reset code for email and returns the email the fake mailer received.
func resetCode(t *testing.T, h *authHarness, b *browser, email string) domain.OTPMail {
	t.Helper()
	if resp, body := b.requestReset(email); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("request reset = %d %s", resp.StatusCode, body)
	}
	return h.mailer.lastReset(t, strings.ToLower(email))
}

func resetFailureReasons(rows []auditRow) []string {
	var out []string
	for _, r := range rows {
		if r.EventType == "auth.otp_verification_failed" {
			out = append(out, r.Reason)
		}
	}
	return out
}

func TestResetChangesThePasswordOnceAndRevokesEverySession(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	phone, laptop := h.newBrowser(t), h.newBrowser(t)
	phone.login(email, "Zq9!kVx1")
	laptop.login(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	b := h.newBrowser(t)
	mail := resetCode(t, h, b, email)
	resp, body := b.confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "Nw7#pLq2")
	if resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("confirm = %d %s", resp.StatusCode, body)
	}
	if b.jar["session"] != "" {
		t.Error("reset does not sign in")
	}
	for _, old := range []*browser{phone, laptop} {
		if resp, _ := old.me(); resp.StatusCode != nethttp.StatusUnauthorized {
			t.Errorf("an old session still works: %d", resp.StatusCode)
		}
	}
	if resp, _ := h.newBrowser(t).login(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Error("the old password must stop working")
	}
	if resp, body := h.newBrowser(t).login(email, "Nw7#pLq2"); resp.StatusCode != nethttp.StatusOK {
		t.Errorf("new password login = %d %s", resp.StatusCode, body)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if got := eventTypes(rows); !slices.Equal(got, []string{"auth.password_reset_succeeded", "auth.sessions_revoked"}) {
		t.Fatalf("audit = %v", got)
	}
	if rows[0].Actor != u.ID || rows[1].Reason != "password_reset" {
		t.Errorf("audit rows = %+v", rows)
	}
	if rows[1].Actor != u.ID {
		t.Errorf("sessions_revoked actor = %q, want the user", rows[1].Actor)
	}
	if h.mailer.changedCount(email) != 1 {
		t.Error("the owner must be told the password changed")
	}
	after, _ := h.user(t, email)
	again, body := h.newBrowser(t).confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "Other#9pw")
	if again.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_reset" {
		t.Errorf("reusing the code = %d %s, want 400 invalid_reset", again.StatusCode, body)
	}
	if now, _ := h.user(t, email); now.PasswordHash != after.PasswordHash {
		t.Error("a used code must not change the password again")
	}
}

// Whoever receives the code owns the mailbox, so a reset also takes back an account that someone
// else registered with this email and never verified.
func TestResetVerifiesAnUnverifiedAccount(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	if resp, body := b.register(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register = %d %s", resp.StatusCode, body)
	}
	u, _ := h.user(t, email)
	if u.Verified {
		t.Fatal("the account must start unverified")
	}

	mail := resetCode(t, h, b, email)
	if resp, body := b.confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "Nw7#pLq2"); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("confirm = %d %s", resp.StatusCode, body)
	}
	if after, _ := h.user(t, email); !after.Verified {
		t.Error("the email must be verified by the reset")
	}
	for _, c := range h.challenges(t, u.ID) {
		if c.Purpose == "email_verification" && c.Open {
			t.Errorf("the verification code %s must be closed", c.ID)
		}
	}
	if resp, body := h.newBrowser(t).login(email, "Nw7#pLq2"); resp.StatusCode != nethttp.StatusOK {
		t.Errorf("login with the new password = %d %s", resp.StatusCode, body)
	}
}

func TestResetWrongCodesCountAndThenLockTheChallenge(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	mail := resetCode(t, h, b, email)
	wrong := wrongCode(mail.OTP.Reveal())

	for i := 1; i <= 5; i++ {
		resp, body := b.confirmReset(mail.ChallengeID.String(), wrong, "Nw7#pLq2")
		if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_reset" {
			t.Fatalf("wrong attempt %d = %d %s", i, resp.StatusCode, body)
		}
	}
	var reset challengeRow
	for _, c := range h.challenges(t, u.ID) {
		if c.ID == mail.ChallengeID.String() {
			reset = c
		}
	}
	if reset.FailedAttempts != 5 {
		t.Fatalf("failed_attempts = %d, want 5", reset.FailedAttempts)
	}
	resp, body := b.confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "Nw7#pLq2")
	if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_reset" {
		t.Errorf("the right code after 5 wrong ones = %d %s, want 400 invalid_reset", resp.StatusCode, body)
	}
	rows := h.auditByTarget(t, mail.ChallengeID.String())
	reasons := resetFailureReasons(rows)
	if want := []string{"otp_mismatch", "otp_mismatch", "otp_mismatch", "otp_mismatch", "otp_mismatch", "too_many_attempts"}; !slices.Equal(reasons, want) {
		t.Errorf("failure reasons = %v, want 5 x otp_mismatch then too_many_attempts", reasons)
	}
	for _, r := range rows {
		if r.Actor != "" {
			t.Errorf("a failed reset must have actor NULL, got %s", r.Actor)
		}
	}
	if after, _ := h.user(t, email); after.PasswordHash != u.PasswordHash {
		t.Error("the password must not change")
	}
}

func TestResetCodeExpiresAfterTenMinutes(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	mail := resetCode(t, h, b, email)

	h.clock.Advance(10 * time.Minute) // exactly at expires_at the code is over
	resp, body := b.confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "Nw7#pLq2")
	if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_reset" {
		t.Fatalf("expired code = %d %s, want 400 invalid_reset", resp.StatusCode, body)
	}
	if got := resetFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); len(got) != 1 || got[0] != "challenge_expired" {
		t.Errorf("failure reasons = %v, want [challenge_expired]", got)
	}

	other := uniqueEmail(t)
	b.signUpAndVerify(other, "Zq9!kVx1")
	last := resetCode(t, h, b, other)
	h.clock.Advance(10*time.Minute - time.Second)
	if resp, body := b.confirmReset(last.ChallengeID.String(), last.OTP.Reveal(), "Nw7#pLq2"); resp.StatusCode != nethttp.StatusNoContent {
		t.Errorf("a code one second before expiry = %d %s, want 204", resp.StatusCode, body)
	}
}

func TestResetOldCodeStopsWorkingAfterANewRequest(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	first := resetCode(t, h, b, email)
	h.clock.Advance(61 * time.Second)
	second := resetCode(t, h, b, email)

	resp, body := b.confirmReset(first.ChallengeID.String(), first.OTP.Reveal(), "Nw7#pLq2")
	if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_reset" {
		t.Fatalf("the first code = %d %s, want 400 invalid_reset", resp.StatusCode, body)
	}
	if got := resetFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); len(got) != 1 || got[0] != "challenge_closed" {
		t.Errorf("failure reasons = %v, want [challenge_closed]", got)
	}
	if resp, body := b.confirmReset(second.ChallengeID.String(), second.OTP.Reveal(), "Nw7#pLq2"); resp.StatusCode != nethttp.StatusNoContent {
		t.Errorf("the second code = %d %s, want 204", resp.StatusCode, body)
	}
}

// The two flows share a table but never each other's challenges.
func TestResetRefusesAVerificationChallenge(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	if resp, body := b.register(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register = %d %s", resp.StatusCode, body)
	}
	verification := h.mailer.lastVerification(t, email)

	resp, body := b.confirmReset(verification.ChallengeID.String(), verification.OTP.Reveal(), "Nw7#pLq2")
	if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_reset" {
		t.Fatalf("reset with a verification code = %d %s, want 400 invalid_reset", resp.StatusCode, body)
	}
	if got := resetFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); len(got) != 1 || got[0] != "challenge_not_found" {
		t.Errorf("failure reasons = %v, want [challenge_not_found]", got)
	}
	if resp, body := b.confirm(verification.ChallengeID.String(), verification.OTP.Reveal()); resp.StatusCode != nethttp.StatusNoContent {
		t.Errorf("the verification challenge must still work: %d %s", resp.StatusCode, body)
	}

	reset := resetCode(t, h, b, email)
	resp, body = b.confirm(reset.ChallengeID.String(), reset.OTP.Reveal())
	if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_verification" {
		t.Errorf("verification with a reset code = %d %s, want 400 invalid_verification", resp.StatusCode, body)
	}
}

func TestResetKeepsALeadingZero(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	mail := resetCode(t, h, b, email)

	userID, err := domain.ParseID("user_id", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := domain.ParseOTP("012345")
	if err != nil {
		t.Fatal(err)
	}
	codec, err := service.NewOTPCodec([]byte(testOTPSecret))
	if err != nil {
		t.Fatal(err)
	}
	digest := codec.Digest(domain.OTPPurposePasswordReset, mail.ChallengeID, userID, code)
	if _, err := h.admin.Exec(context.Background(), `UPDATE app.otp_challenges SET otp_hash = $1 WHERE id = $2`, digest, mail.ChallengeID.String()); err != nil {
		t.Fatal(err)
	}

	resp, body := b.confirmReset(mail.ChallengeID.String(), "12345", "Nw7#pLq2")
	if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_input" {
		t.Errorf("a 5 digit code = %d %s, want 400 invalid_input", resp.StatusCode, body)
	}
	for _, c := range h.challenges(t, u.ID) {
		if c.ID == mail.ChallengeID.String() && c.FailedAttempts != 0 {
			t.Errorf("failed_attempts = %d, want 0: a malformed code is not an attempt", c.FailedAttempts)
		}
	}
	if resp, body := b.confirmReset(mail.ChallengeID.String(), "012345", "Nw7#pLq2"); resp.StatusCode != nethttp.StatusNoContent {
		t.Errorf("a code with a leading zero = %d %s, want 204", resp.StatusCode, body)
	}
}

// The password is judged against the account's email only after the code is right, so a caller who
// guesses a challenge ID learns nothing about the account from the answer.
func TestResetPasswordContainingTheEmailIsCheckedOnlyAfterTheCode(t *testing.T) {
	h := newAuthHarness(t)
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	local := "pw" + hex.EncodeToString(raw[:])
	email := local + "@example.com"
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	mail := resetCode(t, h, b, email)
	password := "Xx!" + local

	resp, body := b.confirmReset(mustTestID(t).String(), "123456", password)
	if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_reset" {
		t.Fatalf("decoy challenge = %d %s, want 400 invalid_reset (not invalid_input)", resp.StatusCode, body)
	}
	resp, body = b.confirmReset(mail.ChallengeID.String(), wrongCode(mail.OTP.Reveal()), password)
	if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_reset" {
		t.Fatalf("wrong code = %d %s, want 400 invalid_reset", resp.StatusCode, body)
	}
	attempts := func() int {
		for _, c := range h.challenges(t, u.ID) {
			if c.ID == mail.ChallengeID.String() {
				if !c.Open {
					t.Error("the challenge must stay open")
				}
				return c.FailedAttempts
			}
		}
		t.Fatal("challenge not found")
		return 0
	}
	if attempts() != 1 {
		t.Fatalf("failed_attempts = %d, want 1", attempts())
	}

	resp, body = b.confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), password)
	got := decodeError(t, body)
	if resp.StatusCode != nethttp.StatusBadRequest || got.Code != "invalid_input" || !slices.Equal(got.Details, []string{"password_contains_email"}) {
		t.Fatalf("right code, password with the email = %d %s, want 400 invalid_input password_contains_email", resp.StatusCode, body)
	}
	if attempts() != 1 {
		t.Errorf("failed_attempts = %d, want 1: the password rule is not an attempt", attempts())
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("audit = %v, want nothing for a refused password", eventTypes(rows))
	}

	if resp, body := b.confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "Nw7#pLq2"); resp.StatusCode != nethttp.StatusNoContent {
		t.Errorf("the same code with another password = %d %s, want 204", resp.StatusCode, body)
	}
}

func TestResetRejectsAWeakPasswordBeforeTheCode(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	mail := resetCode(t, h, b, email)

	resp, body := b.confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "abc")
	got := decodeError(t, body)
	if resp.StatusCode != nethttp.StatusBadRequest || got.Code != "invalid_input" ||
		!slices.Equal(got.Details, []string{"password_too_short", "password_missing_uppercase", "password_missing_special"}) {
		t.Fatalf("weak password = %d %s", resp.StatusCode, body)
	}
	for _, c := range h.challenges(t, u.ID) {
		if c.ID == mail.ChallengeID.String() && (!c.Open || c.FailedAttempts != 0) {
			t.Errorf("challenge = %+v, want it untouched", c)
		}
	}
}

func TestResetConfirmIsLimitedPerIP(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)

	for i := 1; i <= 20; i++ {
		resp, body := b.confirmReset(mustTestID(t).String(), "123456", "Nw7#pLq2")
		if resp.StatusCode != nethttp.StatusBadRequest {
			t.Fatalf("attempt %d = %d %s, want 400", i, resp.StatusCode, body)
		}
	}
	resp, body := b.confirmReset(mustTestID(t).String(), "123456", "Nw7#pLq2")
	if resp.StatusCode != nethttp.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("21st attempt from one IP = %d %s, want 429 with Retry-After", resp.StatusCode, body)
	}
}
