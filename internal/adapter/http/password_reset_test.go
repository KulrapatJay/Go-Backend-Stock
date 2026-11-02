package http

import (
	nethttp "net/http"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestResetRequestSendsACodeToAnAccountWithAPassword(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	resp, body := b.requestReset(email)
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("request = %d %s", resp.StatusCode, body)
	}
	out := decodeAccepted(t, body)
	mail := h.mailer.lastReset(t, email)
	if mail.ChallengeID.String() != out.ChallengeID || out.ExpiresIn != 600 {
		t.Errorf("response %+v and email %s must name the same challenge, valid 600s", out, mail.ChallengeID)
	}
	ch := h.challenges(t, u.ID)
	if last := ch[len(ch)-1]; last.Purpose != "password_reset" || !last.Open || last.ID != out.ChallengeID {
		t.Errorf("challenges = %+v", ch)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.password_reset_requested" || rows[0].Actor != "" || rows[0].TargetID != u.ID {
		t.Errorf("audit = %+v, want password_reset_requested on the user with actor NULL", rows)
	}
}

func TestResetRequestAnswersTheSameForEveryEmail(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)

	verified := uniqueEmail(t)
	b.signUpAndVerify(verified, "Zq9!kVx1")
	unverified := uniqueEmail(t)
	b.register(unverified, "Zq9!kVx1")
	googleOnly := uniqueEmail(t)
	gEmail, err := domain.ParseEmail(googleOnly)
	if err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	if err := h.store.Users().Insert(t.Context(), domain.User{ID: mustTestID(t), Email: gEmail, EmailVerifiedAt: &now, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	bodies := map[string]string{}
	for name, email := range map[string]string{"unknown": uniqueEmail(t), "verified": verified, "unverified": unverified, "google-only": googleOnly} {
		before := h.mailer.resetCount(email)
		resp, body := b.requestReset(email)
		if resp.StatusCode != nethttp.StatusAccepted {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, body)
		}
		decodeAccepted(t, body)
		bodies[name] = stripJSONField(t, body, "challenge_id")

		local := name == "verified" || name == "unverified"
		if sent, want := h.mailer.resetCount(email)-before, map[bool]int{true: 1, false: 0}[local]; sent != want {
			t.Errorf("%s: %d reset codes sent, want %d", name, sent, want)
		}
		if h.mailer.changedCount(email) != 0 {
			t.Errorf("%s: asking for a code must not send a password-changed notice", name)
		}
		rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
		if want := map[bool]int{true: 1, false: 0}[local]; len(rows) != want {
			t.Errorf("%s: audit = %v, want %d rows", name, eventTypes(rows), want)
		}
	}
	for name, body := range bodies {
		if body != bodies["unknown"] {
			t.Errorf("%s body %s differs from the unknown-email body %s", name, body, bodies["unknown"])
		}
	}
}

func TestResetRequestReplacesTheOpenCode(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	if resp, body := b.requestReset(email); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("first request = %d %s", resp.StatusCode, body)
	}
	first := h.mailer.lastReset(t, email)
	h.clock.Advance(61 * time.Second)
	if resp, body := b.requestReset(email); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("second request = %d %s", resp.StatusCode, body)
	}
	second := h.mailer.lastReset(t, email)

	if first.ChallengeID == second.ChallengeID || h.mailer.resetCount(email) != 2 {
		t.Errorf("want two codes mailed with different challenge IDs, got %s and %s (%d mails)",
			first.ChallengeID, second.ChallengeID, h.mailer.resetCount(email))
	}
	var resets []challengeRow
	for _, c := range h.challenges(t, u.ID) {
		if c.Purpose == "password_reset" {
			resets = append(resets, c)
		}
	}
	if len(resets) != 2 || resets[0].Open || !resets[1].Open || resets[1].ID != second.ChallengeID.String() {
		t.Errorf("reset challenges = %+v, want the first closed and the second open", resets)
	}
}

func TestResetRequestCooldownAndHourlyQuota(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")

	// Register just used the verification scope for this email: the reset scope is separate.
	if resp, body := b.requestReset(email); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("first request = %d %s, want 202 right after register", resp.StatusCode, body)
	}
	resp, body := b.requestReset(email)
	if resp.StatusCode != nethttp.StatusTooManyRequests || decodeError(t, body).Code != "rate_limited" || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("second request within the cooldown = %d %s, want 429 with Retry-After", resp.StatusCode, body)
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 1 || rows[0].Metadata["scope"] != "password_reset_request_email" {
		t.Errorf("audit = %+v, want one denial naming password_reset_request_email", rows)
	}

	// And the reset cooldown does not block asking for a verification code.
	h.clock.Advance(61 * time.Second)
	if resp, body := b.requestReset(email); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("request after the cooldown = %d %s", resp.StatusCode, body)
	}
	if resp, body := b.requestCode(email); resp.StatusCode != nethttp.StatusAccepted {
		t.Errorf("verification request after a reset request = %d %s, want 202", resp.StatusCode, body)
	}

	for i := 3; i <= 5; i++ {
		h.clock.Advance(61 * time.Second)
		if resp, body := b.requestReset(email); resp.StatusCode != nethttp.StatusAccepted {
			t.Fatalf("request %d = %d %s, want 202", i, resp.StatusCode, body)
		}
	}
	h.clock.Advance(61 * time.Second)
	if resp, body := b.requestReset(email); resp.StatusCode != nethttp.StatusTooManyRequests {
		t.Errorf("6th request within the hour = %d %s, want 429", resp.StatusCode, body)
	}
}

func TestResetRequestCountsUnknownEmailsAgainstTheIP(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)

	for i := 1; i <= 20; i++ {
		if resp, body := b.requestReset(uniqueEmail(t)); resp.StatusCode != nethttp.StatusAccepted {
			t.Fatalf("request %d = %d %s, want 202", i, resp.StatusCode, body)
		}
	}
	resp, body := b.requestReset(uniqueEmail(t))
	if resp.StatusCode != nethttp.StatusTooManyRequests {
		t.Errorf("21st request from one IP = %d %s, want 429", resp.StatusCode, body)
	}
}
