package http

import (
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
)

// challengeByID returns one challenge of the user, failing the test when it is missing.
func (h *authHarness) challengeByID(t *testing.T, userID, challengeID string) challengeRow {
	t.Helper()
	for _, c := range h.challenges(t, userID) {
		if c.ID == challengeID {
			return c
		}
	}
	t.Fatalf("challenge %s not found for user %s", challengeID, userID)
	return challengeRow{}
}

func countEvents(rows []auditRow, eventType string) int {
	n := 0
	for _, r := range rows {
		if r.EventType == eventType {
			n++
		}
	}
	return n
}

// Two browsers confirm the same challenge with the same right code but different new passwords at
// once. The challenge row lock (FOR UPDATE) lets exactly one in: the other then sees a consumed
// challenge. Without it both could pass the check and the last SetPassword would win silently,
// leaving an audit trail with two successes for one code.
func TestConcurrentResetsWithTheRightCodeSucceedOnce(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	mail := resetCode(t, h, h.newBrowser(t), email)
	passwords := []string{"Nw7#pLq2", "Rt5$mKd8"}

	browsers := make([]*browser, len(passwords))
	for i := range browsers {
		browsers[i] = h.newBrowser(t)
		browsers[i].fetchCSRF()
	}
	var wg sync.WaitGroup
	statuses := make([]int, len(browsers))
	bodies := make([][]byte, len(browsers))
	errs := make([]error, len(browsers))
	for i, b := range browsers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, body, err := b.tryDo(nethttp.MethodPost, "/api/v1/auth/password-reset/confirm",
				map[string]string{"challenge_id": mail.ChallengeID.String(), "otp": mail.OTP.Reveal(), "new_password": passwords[i]}, nil)
			statuses[i], bodies[i], errs[i] = resp.StatusCode, body, err
		}()
	}
	wg.Wait()

	winner := -1
	rejected := 0
	for i := range browsers {
		if errs[i] != nil {
			t.Fatalf("request %d: %v", i, errs[i])
		}
		switch statuses[i] {
		case nethttp.StatusNoContent:
			if winner != -1 {
				t.Errorf("both resets succeeded")
			}
			winner = i
		case nethttp.StatusBadRequest:
			if code := decodeError(t, bodies[i]).Code; code != "invalid_reset" {
				t.Errorf("request %d error = %s, want invalid_reset", i, code)
			}
			rejected++
		default:
			t.Errorf("request %d = %d %s, want 204 or 400", i, statuses[i], bodies[i])
		}
	}
	if winner == -1 || rejected != 1 {
		t.Fatalf("statuses = %v, want exactly one 204 and one 400", statuses)
	}
	if got := countEvents(h.auditByTarget(t, u.ID), "auth.password_reset_succeeded"); got != 1 {
		t.Errorf("auth.password_reset_succeeded rows = %d, want 1", got)
	}
	for i, password := range passwords {
		want := nethttp.StatusUnauthorized
		if i == winner {
			want = nethttp.StatusOK
		}
		if resp, body := h.newBrowser(t).login(email, password); resp.StatusCode != want {
			t.Errorf("login with password %d = %d %s, want %d", i, resp.StatusCode, body, want)
		}
	}
	if got := h.mailer.changedCount(email); got != 1 {
		t.Errorf("password changed notices = %d, want 1", got)
	}
}

// A login that checks the old password while a reset commits must not leave a live session: the
// reset locks the user and revokes every session, and a login either runs before (its session is
// revoked) or after (the old password no longer matches). Each round uses a fresh account; the
// invariant is asserted on the outcome, not on which side won.
func TestLoginRacingAResetLeavesNoOldPasswordSession(t *testing.T) {
	h := newAuthHarness(t)
	for round := 0; round < 10; round++ {
		email := uniqueEmail(t)
		h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
		mail := resetCode(t, h, h.newBrowser(t), email)

		loginer, resetter := h.newBrowser(t), h.newBrowser(t)
		loginer.fetchCSRF()
		resetter.fetchCSRF()
		var wg sync.WaitGroup
		var loginStatus, resetStatus int
		var loginErr, resetErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			resp, _, err := loginer.tryDo(nethttp.MethodPost, "/api/v1/auth/login",
				map[string]string{"email": email, "password": "Zq9!kVx1"}, nil)
			loginStatus, loginErr = resp.StatusCode, err
		}()
		go func() {
			defer wg.Done()
			resp, _, err := resetter.tryDo(nethttp.MethodPost, "/api/v1/auth/password-reset/confirm",
				map[string]string{"challenge_id": mail.ChallengeID.String(), "otp": mail.OTP.Reveal(), "new_password": "Nw7#pLq2"}, nil)
			resetStatus, resetErr = resp.StatusCode, err
		}()
		wg.Wait()

		if loginErr != nil || resetErr != nil {
			t.Fatalf("round %d: login error %v, reset error %v", round, loginErr, resetErr)
		}
		if resetStatus != nethttp.StatusNoContent {
			t.Fatalf("round %d: reset = %d, want 204", round, resetStatus)
		}
		switch loginStatus {
		case nethttp.StatusUnauthorized:
			// The reset won: the old password no longer matched.
		case nethttp.StatusOK:
			if resp, body := loginer.me(); resp.StatusCode != nethttp.StatusUnauthorized {
				t.Fatalf("round %d: a login with the old password survived the reset: me = %d %s", round, resp.StatusCode, body)
			}
		default:
			t.Fatalf("round %d: login = %d, want 200 or 401", round, loginStatus)
		}
		u, _ := h.user(t, email)
		var live int
		if err := h.admin.QueryRow(t.Context(),
			`SELECT count(*) FROM app.sessions WHERE user_id = $1 AND revoked_at IS NULL`, u.ID).Scan(&live); err != nil {
			t.Fatal(err)
		}
		if live != 0 {
			t.Fatalf("round %d: %d live session(s) remain after the reset", round, live)
		}
	}
}

// Eight concurrent wrong codes against a limit of five, as for email verification: the challenge
// lock serializes the attempt check and RecordFailure, so exactly five are counted as otp_mismatch
// and the other three see a full counter. Each comes from its own IP and the audit trail must
// agree with the counter.
func TestConcurrentWrongResetCodesAreAllCounted(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	mail := resetCode(t, h, h.newBrowser(t), email)
	wrong := wrongCode(mail.OTP.Reveal())

	browsers := make([]*browser, 8)
	for i := range browsers {
		browsers[i] = h.newBrowser(t)
		browsers[i].fetchCSRF()
	}
	var wg sync.WaitGroup
	statuses := make([]int, len(browsers))
	bodies := make([][]byte, len(browsers))
	errs := make([]error, len(browsers))
	for i, b := range browsers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, body, err := b.tryDo(nethttp.MethodPost, "/api/v1/auth/password-reset/confirm",
				map[string]string{"challenge_id": mail.ChallengeID.String(), "otp": wrong, "new_password": "Nw7#pLq2"}, nil)
			statuses[i], bodies[i], errs[i] = resp.StatusCode, body, err
		}()
	}
	wg.Wait()
	for i := range browsers {
		if errs[i] != nil {
			t.Errorf("request %d: %v", i, errs[i])
		} else if statuses[i] != nethttp.StatusBadRequest || decodeError(t, bodies[i]).Code != "invalid_reset" {
			t.Errorf("request %d = %d %s, want 400 invalid_reset", i, statuses[i], bodies[i])
		}
	}
	if got := h.challengeByID(t, u.ID, mail.ChallengeID.String()).FailedAttempts; got != 5 {
		t.Errorf("failed_attempts = %d, want exactly 5", got)
	}
	counts := map[string]int{}
	for _, reason := range resetFailureReasons(h.auditByTarget(t, mail.ChallengeID.String())) {
		counts[reason]++
	}
	if counts["otp_mismatch"] != 5 || counts["too_many_attempts"] != 3 || len(counts) != 2 {
		t.Errorf("failure audits by reason = %v, want 5 x otp_mismatch and 3 x too_many_attempts", counts)
	}
}

// A reset whose success audit cannot be written rolls everything back: the password, the consumed
// code, the revoked sessions and the notice. The twin shares the database with h but has its own
// clock and mailer; both clocks start at the real now, far inside the 10 minute life of the code.
func TestResetWithoutItsAuditChangesNothing(t *testing.T) {
	h, failing := failingTwin(t)
	email := uniqueEmail(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	owner := h.newBrowser(t)
	if resp, body := owner.login(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	u, _ := h.user(t, email)
	mail := resetCode(t, h, h.newBrowser(t), email)

	resp, body := failing.newBrowser(t).confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "Nw7#pLq2")
	requireAuditFailure(t, "reset", resp, body)

	if after, _ := h.user(t, email); after.PasswordHash != u.PasswordHash {
		t.Error("the password must not change without its audit")
	}
	if c := h.challengeByID(t, u.ID, mail.ChallengeID.String()); !c.Open || c.FailedAttempts != 0 {
		t.Errorf("challenge = %+v, want it open and untouched", c)
	}
	if resp, body := owner.me(); resp.StatusCode != nethttp.StatusOK {
		t.Errorf("the old session must survive a rolled-back reset: me = %d %s", resp.StatusCode, body)
	}
	if got := failing.mailer.changedCount(email); got != 0 {
		t.Errorf("password changed notices = %d, want 0", got)
	}
	if got := countEvents(h.auditByTarget(t, u.ID), "auth.password_reset_succeeded"); got != 0 {
		t.Errorf("auth.password_reset_succeeded rows = %d, want 0", got)
	}
}

func TestResetLogsHoldNoSecrets(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	owner := h.newBrowser(t)
	if resp, body := owner.login(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	oldSession := owner.jar["session"]
	const newPassword = "Nw7#pLq2"

	b := h.newBrowser(t)
	mail := resetCode(t, h, b, email)
	if resp, body := b.confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), newPassword); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("confirm = %d %s", resp.StatusCode, body)
	}

	logs := h.logs.String()
	if !strings.Contains(logs, `"route":"/api/v1/auth/password-reset/confirm"`) {
		t.Fatal("the access log must be captured, or this test proves nothing")
	}
	secrets := map[string]string{
		"otp": mail.OTP.Reveal(), "new password": newPassword, "old password": "Zq9!kVx1",
		"old session": oldSession, "email": email,
	}
	for name, secret := range secrets {
		if secret == "" {
			t.Errorf("the %s is empty, so its check proves nothing", name)
		} else if strings.Contains(logs, secret) {
			t.Errorf("logs contain the %s", name)
		}
	}
}
