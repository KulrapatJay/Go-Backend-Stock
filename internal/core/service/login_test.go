package service

import (
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// With the only hasher slot taken, a login that hashes waits and gets server_busy, while one that
// does not hash answers at once. That shows which paths spend an Argon2 verification.
func TestLoginSpendsAHashWhenThereIsNoPasswordToCheck(t *testing.T) {
	hasher, _ := newHasher(t, testArgon2Params, 1, 20*time.Millisecond)
	ta := newTestAuth(t, func(d *AuthDeps) { d.Hasher = hasher })
	unknown := mustEmail(t, "nobody@example.com")
	googleOnly := mustEmail(t, "google-only@example.com")
	ta.repos.users.credentials[googleOnly.String()] = domain.UserCredentials{User: domain.User{ID: mustID(t), Email: googleOnly}}
	release := hold(t, hasher)
	defer release()

	for _, email := range []domain.Email{unknown, googleOnly} {
		_, err := ta.auth.Login(t.Context(), domain.LoginInput{Email: email, Password: domain.NewSecretText("Zq9!kVx1")}, testMeta(t))
		_ = requireCode(t, err, domain.CodeServerBusy)
	}
	_, err := ta.auth.Login(t.Context(), domain.LoginInput{Email: unknown, Password: domain.NewSecretText(strings.Repeat("a", 32))}, testMeta(t))
	_ = requireCode(t, err, domain.CodeServerBusy) // 32 characters is still a password worth checking
}

func TestLoginRefusesAnOverlongPasswordWithoutHashing(t *testing.T) {
	hasher, _ := newHasher(t, testArgon2Params, 1, 20*time.Millisecond)
	ta := newTestAuth(t, func(d *AuthDeps) { d.Hasher = hasher })
	release := hold(t, hasher)
	defer release()

	for _, pw := range []string{strings.Repeat("a", 33), ""} {
		_, err := ta.auth.Login(t.Context(), domain.LoginInput{Email: mustEmail(t, "nobody@example.com"), Password: domain.NewSecretText(pw)}, testMeta(t))
		_ = requireCode(t, err, domain.CodeInvalidCredentials)
	}
	events := ta.uow.audit.events
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want one login_failed per attempt", len(events))
	}
	for _, e := range events {
		if e.EventType != domain.EventAuthLoginFailed || e.ReasonCode != "invalid_credentials" || !e.ActorUserID.IsZero() || e.TargetType != "login" {
			t.Errorf("event = %+v", e)
		}
	}
}

func TestLogoutWithoutASessionDoesNothing(t *testing.T) {
	ta := newTestAuth(t, nil)
	if err := ta.auth.Logout(t.Context(), domain.Principal{}, testMeta(t)); err != nil {
		t.Fatalf("Logout(no session) = %v, want nil", err)
	}
	if ta.uow.runs != 0 {
		t.Error("logout without a session must not open a transaction")
	}
}
