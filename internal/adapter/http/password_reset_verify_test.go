package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"maps"
	nethttp "net/http"
	"slices"
	"sync"
	"testing"
	"time"
)

// The two-step password reset (ADR-0022): verify turns a right code into a reset token, and confirm
// spends the token instead of the code.

func (b *browser) verifyReset(challengeID, otp string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/password-reset/verify", map[string]string{"challenge_id": challengeID, "otp": otp}, nil)
}

func (b *browser) confirmResetWithToken(token, newPassword string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/password-reset/confirm",
		map[string]string{"reset_token": token, "new_password": newPassword}, nil)
}

// resetToken verifies the code with b and returns the reset token.
func resetToken(t *testing.T, b *browser, challengeID, otp string) string {
	t.Helper()
	resp, body := b.verifyReset(challengeID, otp)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("verify = %d %s", resp.StatusCode, body)
	}
	var got ResetTokenResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	return got.ResetToken
}

func tokenFailureReasons(rows []auditRow) []string {
	var out []string
	for _, r := range rows {
		if r.EventType == "auth.password_reset_failed" {
			out = append(out, r.Reason)
		}
	}
	return out
}

func wantInvalidReset(t *testing.T, what string, resp response, body []byte) {
	t.Helper()
	if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_reset" {
		t.Fatalf("%s = %d %s, want 400 invalid_reset", what, resp.StatusCode, body)
	}
}

func TestResetTwoStepsChangeThePasswordOnce(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	phone := h.newBrowser(t)
	phone.login(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	b := h.newBrowser(t)
	mail := resetCode(t, h, b, email)

	resp, body := b.verifyReset(mail.ChallengeID.String(), mail.OTP.Reveal())
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("verify = %d %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: the body is a credential", got)
	}
	var grant ResetTokenResponse
	if err := json.Unmarshal(body, &grant); err != nil {
		t.Fatal(err)
	}
	if len(grant.ResetToken) != 43 || grant.ExpiresIn != 600 {
		t.Fatalf("verify body = %s, want a 43-character token that lasts 600 seconds", body)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.password_reset_verified" || rows[0].Actor != u.ID || rows[0].TargetID != mail.ChallengeID.String() {
		t.Fatalf("verify audit = %+v, want one auth.password_reset_verified by the owner", rows)
	}
	var stored []byte
	if err := h.admin.QueryRow(context.Background(), `SELECT reset_token_hash FROM app.otp_challenges WHERE id = $1`, mail.ChallengeID.String()).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 32 || slices.Equal(stored, []byte(grant.ResetToken)) {
		t.Error("only a 32-byte hash of the token may be stored")
	}
	if !h.challengeByID(t, u.ID, mail.ChallengeID.String()).Open {
		t.Error("verify must leave the challenge open until confirm")
	}
	if after, _ := h.user(t, email); after.PasswordHash != u.PasswordHash {
		t.Fatal("verify must not change the password")
	}

	// The code is spent: neither step accepts it again.
	again, body := b.verifyReset(mail.ChallengeID.String(), mail.OTP.Reveal())
	wantInvalidReset(t, "verify the same code again", again, body)
	oneStep, body := b.confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "Other#9pw")
	wantInvalidReset(t, "one-step confirm with a verified code", oneStep, body)
	for _, r := range []response{again, oneStep} {
		if got := resetFailureReasons(h.auditFor(t, r.Header.Get("X-Request-ID"))); !slices.Equal(got, []string{"challenge_closed"}) {
			t.Errorf("failure reasons = %v, want [challenge_closed]", got)
		}
	}

	resp, body = b.confirmResetWithToken(grant.ResetToken, "Nw7#pLq2")
	if resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("confirm with token = %d %s", resp.StatusCode, body)
	}
	rows = h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if got := eventTypes(rows); !slices.Equal(got, []string{"auth.password_reset_succeeded", "auth.sessions_revoked"}) {
		t.Fatalf("confirm audit = %v", got)
	}
	if rows[0].Actor != u.ID || rows[1].Reason != "password_reset" {
		t.Errorf("confirm audit rows = %+v", rows)
	}
	if resp, _ := phone.me(); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("an old session still works: %d", resp.StatusCode)
	}
	if h.mailer.changedCount(email) != 1 {
		t.Error("the owner must be told the password changed")
	}
	if resp, body := h.newBrowser(t).login(email, "Nw7#pLq2"); resp.StatusCode != nethttp.StatusOK {
		t.Errorf("new password login = %d %s", resp.StatusCode, body)
	}

	after, _ := h.user(t, email)
	reuse, body := b.confirmResetWithToken(grant.ResetToken, "Other#9pw")
	wantInvalidReset(t, "reusing the token", reuse, body)
	if got := tokenFailureReasons(h.auditFor(t, reuse.Header.Get("X-Request-ID"))); !slices.Equal(got, []string{"challenge_closed"}) {
		t.Errorf("token failure reasons = %v, want [challenge_closed]", got)
	}
	if now, _ := h.user(t, email); now.PasswordHash != after.PasswordHash {
		t.Error("a used token must not change the password again")
	}
}

// verify and the one-step confirm count wrong codes against the same 5 attempts.
func TestResetVerifyCountsWrongCodesWithConfirm(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	mail := resetCode(t, h, b, email)
	wrong := wrongCode(mail.OTP.Reveal())

	for i := 1; i <= 3; i++ {
		resp, body := b.verifyReset(mail.ChallengeID.String(), wrong)
		wantInvalidReset(t, "wrong verify", resp, body)
		if got := resetFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); !slices.Equal(got, []string{"otp_mismatch"}) {
			t.Fatalf("attempt %d reasons = %v, want [otp_mismatch]", i, got)
		}
	}
	for i := 1; i <= 2; i++ {
		resp, body := b.confirmReset(mail.ChallengeID.String(), wrong, "Nw7#pLq2")
		wantInvalidReset(t, "wrong confirm", resp, body)
	}
	if got := h.challengeByID(t, u.ID, mail.ChallengeID.String()).FailedAttempts; got != 5 {
		t.Fatalf("failed_attempts = %d, want 5", got)
	}
	resp, body := b.verifyReset(mail.ChallengeID.String(), mail.OTP.Reveal())
	wantInvalidReset(t, "the right code after 5 wrong ones", resp, body)
	if got := resetFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); !slices.Equal(got, []string{"too_many_attempts"}) {
		t.Errorf("reasons = %v, want [too_many_attempts]", got)
	}
	for _, r := range h.auditByTarget(t, mail.ChallengeID.String()) {
		if r.Actor != "" {
			t.Errorf("a failed verify must have actor NULL, got %s", r.Actor)
		}
	}
}

func TestResetVerifyRefusesExpiredUnknownAndVerificationCodes(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	if resp, body := b.register(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register = %d %s", resp.StatusCode, body)
	}
	verification := h.mailer.lastVerification(t, email)
	mail := resetCode(t, h, b, email)

	cases := []struct {
		name, challengeID, otp, reason string
	}{
		{"unknown challenge (decoy)", mustTestID(t).String(), "123456", "challenge_not_found"},
		{"an email verification code", verification.ChallengeID.String(), verification.OTP.Reveal(), "challenge_not_found"},
	}
	for _, c := range cases {
		resp, body := b.verifyReset(c.challengeID, c.otp)
		wantInvalidReset(t, c.name, resp, body)
		if got := resetFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); !slices.Equal(got, []string{c.reason}) {
			t.Errorf("%s: reasons = %v, want [%s]", c.name, got, c.reason)
		}
	}
	if resp, body := b.verifyReset(mail.ChallengeID.String(), "12345"); resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_input" {
		t.Errorf("a 5 digit code = %d %s, want 400 invalid_input", resp.StatusCode, body)
	}

	h.clock.Advance(10 * time.Minute) // exactly at expires_at the code is over
	resp, body := b.verifyReset(mail.ChallengeID.String(), mail.OTP.Reveal())
	wantInvalidReset(t, "an expired code", resp, body)
	if got := resetFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); !slices.Equal(got, []string{"challenge_expired"}) {
		t.Errorf("reasons = %v, want [challenge_expired]", got)
	}
}

// The token has its own 10 minutes from verify, so a code verified just before it expires still
// leaves time to choose a password.
func TestResetTokenLastsTenMinutesFromVerify(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)

	late := uniqueEmail(t)
	b.signUpAndVerify(late, "Zq9!kVx1")
	mail := resetCode(t, h, b, late)
	h.clock.Advance(9 * time.Minute)
	token := resetToken(t, b, mail.ChallengeID.String(), mail.OTP.Reveal())
	h.clock.Advance(10*time.Minute - time.Second) // the code expired long ago, the token has not
	if resp, body := b.confirmResetWithToken(token, "Nw7#pLq2"); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("a token one second before expiry = %d %s, want 204", resp.StatusCode, body)
	}

	other := uniqueEmail(t)
	b.signUpAndVerify(other, "Zq9!kVx1")
	mail = resetCode(t, h, b, other)
	token = resetToken(t, b, mail.ChallengeID.String(), mail.OTP.Reveal())
	h.clock.Advance(10 * time.Minute)
	resp, body := b.confirmResetWithToken(token, "Nw7#pLq2")
	wantInvalidReset(t, "an expired token", resp, body)
	if got := tokenFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); !slices.Equal(got, []string{"reset_token_expired"}) {
		t.Errorf("reasons = %v, want [reset_token_expired]", got)
	}
}

// Asking for a new code invalidates the open challenge, and with it a token already issued for it.
func TestResetTokenStopsWorkingAfterANewRequest(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	first := resetCode(t, h, b, email)
	token := resetToken(t, b, first.ChallengeID.String(), first.OTP.Reveal())
	h.clock.Advance(61 * time.Second)
	second := resetCode(t, h, b, email)

	resp, body := b.confirmResetWithToken(token, "Nw7#pLq2")
	wantInvalidReset(t, "the token of the first code", resp, body)
	if got := tokenFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); !slices.Equal(got, []string{"challenge_closed"}) {
		t.Errorf("reasons = %v, want [challenge_closed]", got)
	}
	newToken := resetToken(t, b, second.ChallengeID.String(), second.OTP.Reveal())
	if resp, body := b.confirmResetWithToken(newToken, "Nw7#pLq2"); resp.StatusCode != nethttp.StatusNoContent {
		t.Errorf("the token of the second code = %d %s, want 204", resp.StatusCode, body)
	}
}

// The token is tied to the CSRF binding of the browser that verified the code: a token copied to
// another browser is refused, and the refusal does not burn it for its owner.
func TestResetTokenWorksOnlyInTheBrowserThatVerified(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	mail := resetCode(t, h, b, email)
	token := resetToken(t, b, mail.ChallengeID.String(), mail.OTP.Reveal())

	resp, body := h.newBrowser(t).confirmResetWithToken(token, "Ev1l#pass")
	wantInvalidReset(t, "the token from another browser", resp, body)
	if got := tokenFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); !slices.Equal(got, []string{"browser_mismatch"}) {
		t.Errorf("reasons = %v, want [browser_mismatch]", got)
	}
	if after, _ := h.user(t, email); after.PasswordHash != u.PasswordHash {
		t.Fatal("another browser must not change the password")
	}
	if resp, body := b.confirmResetWithToken(token, "Nw7#pLq2"); resp.StatusCode != nethttp.StatusNoContent {
		t.Errorf("the verifying browser = %d %s, want 204", resp.StatusCode, body)
	}
}

func TestResetConfirmTakesEitherTheTokenOrTheCode(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	mail := resetCode(t, h, b, email)
	token := resetToken(t, b, mail.ChallengeID.String(), mail.OTP.Reveal())

	for name, req := range map[string]map[string]string{
		"both":    {"reset_token": token, "challenge_id": mail.ChallengeID.String(), "otp": mail.OTP.Reveal(), "new_password": "Nw7#pLq2"},
		"neither": {"new_password": "Nw7#pLq2"},
	} {
		b.fetchCSRF()
		resp, body := b.do(nethttp.MethodPost, "/api/v1/auth/password-reset/confirm", req, nil)
		if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_input" {
			t.Errorf("%s = %d %s, want 400 invalid_input", name, resp.StatusCode, body)
		}
		if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 0 {
			t.Errorf("%s: audit = %v, want nothing for a malformed body", name, eventTypes(rows))
		}
	}
	for name, bad := range map[string]string{"malformed": "not-a-token", "unknown": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		resp, body := b.confirmResetWithToken(bad, "Nw7#pLq2")
		wantInvalidReset(t, name+" token", resp, body)
		if got := tokenFailureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); !slices.Equal(got, []string{"reset_token_not_found"}) {
			t.Errorf("%s: reasons = %v, want [reset_token_not_found]", name, got)
		}
	}
	if resp, body := b.confirmResetWithToken(token, "Nw7#pLq2"); resp.StatusCode != nethttp.StatusNoContent {
		t.Errorf("the real token = %d %s, want 204", resp.StatusCode, body)
	}
}

// As with the code: the email rule is judged only after the token is accepted, and failing it
// spends nothing, so the owner can choose another password with the same token.
func TestResetTokenWithAPasswordContainingTheEmailKeepsTheToken(t *testing.T) {
	h := newAuthHarness(t)
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	local := "pw" + hex.EncodeToString(raw[:])
	email := local + "@example.com"
	b := h.newBrowser(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	mail := resetCode(t, h, b, email)
	token := resetToken(t, b, mail.ChallengeID.String(), mail.OTP.Reveal())

	// An unknown token with the same password says invalid_reset, not the email rule.
	resp, body := b.confirmResetWithToken("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "Xx!"+local)
	wantInvalidReset(t, "unknown token", resp, body)

	resp, body = b.confirmResetWithToken(token, "Xx!"+local)
	got := decodeError(t, body)
	if resp.StatusCode != nethttp.StatusBadRequest || got.Code != "invalid_input" || !slices.Equal(got.Details, []string{"password_contains_email"}) {
		t.Fatalf("password with the email = %d %s, want 400 invalid_input password_contains_email", resp.StatusCode, body)
	}
	if resp, body := b.confirmResetWithToken(token, "Nw7#pLq2"); resp.StatusCode != nethttp.StatusNoContent {
		t.Errorf("the same token with another password = %d %s, want 204", resp.StatusCode, body)
	}
}

func TestResetVerifyAndConfirmShareTheIPLimit(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)

	for i := 1; i <= 20; i++ {
		var resp response
		var body []byte
		if i%2 == 0 {
			resp, body = b.verifyReset(mustTestID(t).String(), "123456")
		} else {
			resp, body = b.confirmResetWithToken("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "Nw7#pLq2")
		}
		if resp.StatusCode != nethttp.StatusBadRequest {
			t.Fatalf("attempt %d = %d %s, want 400", i, resp.StatusCode, body)
		}
	}
	for name, try := range map[string]func() (response, []byte){
		"verify":  func() (response, []byte) { return b.verifyReset(mustTestID(t).String(), "123456") },
		"confirm": func() (response, []byte) { return b.confirmReset(mustTestID(t).String(), "123456", "Nw7#pLq2") },
	} {
		if resp, body := try(); resp.StatusCode != nethttp.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
			t.Errorf("%s after 20 attempts from one IP = %d %s, want 429 with Retry-After", name, resp.StatusCode, body)
		}
	}
}

// raceResets sends the same request from every browser at once and returns the statuses.
func raceResets(t *testing.T, browsers []*browser, path string, body func(i int) map[string]string) []int {
	t.Helper()
	var wg sync.WaitGroup
	statuses := make([]int, len(browsers))
	errs := make([]error, len(browsers))
	for i, b := range browsers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _, err := b.tryDo(nethttp.MethodPost, path, body(i), nil)
			statuses[i], errs[i] = resp.StatusCode, err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	return statuses
}

func countStatus(statuses []int, want int) int {
	n := 0
	for _, s := range statuses {
		if s == want {
			n++
		}
	}
	return n
}

// The challenge row lock lets exactly one of several concurrent verifies of the same right code
// through, and one of several concurrent confirms with the same token.
func TestConcurrentVerifiesAndTokenConfirmsSucceedOnce(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	mail := resetCode(t, h, h.newBrowser(t), email)

	verifiers := make([]*browser, 3)
	for i := range verifiers {
		verifiers[i] = h.newBrowser(t)
		verifiers[i].fetchCSRF()
	}
	statuses := raceResets(t, verifiers, "/api/v1/auth/password-reset/verify", func(int) map[string]string {
		return map[string]string{"challenge_id": mail.ChallengeID.String(), "otp": mail.OTP.Reveal()}
	})
	if countStatus(statuses, nethttp.StatusOK) != 1 || countStatus(statuses, nethttp.StatusBadRequest) != 2 {
		t.Fatalf("verify statuses = %v, want one 200 and two 400", statuses)
	}
	if got := countEvents(h.auditByTarget(t, mail.ChallengeID.String()), "auth.password_reset_verified"); got != 1 {
		t.Errorf("auth.password_reset_verified rows = %d, want 1", got)
	}

	// One browser verifies a fresh code and fires several confirms with its token at once.
	h.clock.Advance(61 * time.Second)
	b := h.newBrowser(t)
	fresh := resetCode(t, h, b, email)
	token := resetToken(t, b, fresh.ChallengeID.String(), fresh.OTP.Reveal())
	b.fetchCSRF()
	// Copies of b: the same cookies, CSRF token and IP, but their own cookie jars, so the
	// goroutines do not share a map.
	same := make([]*browser, 3)
	for i := range same {
		c := *b
		c.jar = maps.Clone(b.jar)
		same[i] = &c
	}
	passwords := []string{"Nw7#pLq2", "Rt5$mKd8", "Qp3%vLs9"}
	statuses = raceResets(t, same, "/api/v1/auth/password-reset/confirm", func(i int) map[string]string {
		return map[string]string{"reset_token": token, "new_password": passwords[i]}
	})
	if countStatus(statuses, nethttp.StatusNoContent) != 1 || countStatus(statuses, nethttp.StatusBadRequest) != 2 {
		t.Fatalf("confirm statuses = %v, want one 204 and two 400", statuses)
	}
	if got := countEvents(h.auditByTarget(t, u.ID), "auth.password_reset_succeeded"); got != 1 {
		t.Errorf("auth.password_reset_succeeded rows = %d, want 1", got)
	}
}
