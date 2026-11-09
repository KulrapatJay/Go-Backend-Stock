package postgres

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// The two-step password reset (ADR-0022) stores its grant on the challenge row.

func newGrant(t *testing.T, verifiedAt time.Time) domain.ResetGrant {
	t.Helper()
	g, err := domain.NewResetGrant(domain.ResetGrant{
		TokenHash: randomHash(t), BindingHash: randomHash(t), VerifiedAt: verifiedAt, ExpiresAt: verifiedAt.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestResetGrantIsStoredOnceAndFoundByItsTokenHash(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)
	g := newGrant(t, now())

	if err := store.OTPChallenges().GrantReset(t.Context(), c.ID, g); err != nil {
		t.Fatalf("GrantReset: %v", err)
	}
	if err := store.OTPChallenges().GrantReset(t.Context(), c.ID, newGrant(t, now())); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a second GrantReset error = %v, want not_found: a code is verified once", err)
	}
	got, err := store.OTPChallenges().ByResetTokenHash(t.Context(), g.TokenHash)
	if err != nil || got.ID != c.ID || got.Grant == nil || !bytes.Equal(got.Grant.TokenHash, g.TokenHash) ||
		!bytes.Equal(got.Grant.BindingHash, g.BindingHash) || !got.Grant.VerifiedAt.Equal(g.VerifiedAt) || !got.Grant.ExpiresAt.Equal(g.ExpiresAt) {
		t.Fatalf("ByResetTokenHash = %+v, %v; want the challenge with its grant", got, err)
	}
	if got.ConsumedAt != nil || got.InvalidatedAt != nil {
		t.Error("a grant leaves the challenge open")
	}
	if _, err := store.OTPChallenges().ByResetTokenHash(t.Context(), randomHash(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByResetTokenHash(unknown) error = %v, want not_found", err)
	}
	// A challenge without a grant still reads back with none.
	plain := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)
	if got, err := store.OTPChallenges().ByID(t.Context(), plain.Purpose, plain.ID); err != nil || got.Grant != nil {
		t.Errorf("ByID(no grant) = %+v, %v", got, err)
	}
}

func TestResetGrantOnlyOnAnOpenUnexpiredPasswordResetChallenge(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	verification := insertChallenge(t, store, user.ID, domain.OTPPurposeEmailVerification)
	if err := store.OTPChallenges().GrantReset(t.Context(), verification.ID, newGrant(t, now())); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GrantReset(email verification) error = %v, want not_found", err)
	}

	expired := newChallenge(t, insertUser(t, store).ID, domain.OTPPurposePasswordReset, now().Add(-time.Hour))
	if err := store.OTPChallenges().Insert(t.Context(), expired); err != nil {
		t.Fatal(err)
	}
	if err := store.OTPChallenges().GrantReset(t.Context(), expired.ID, newGrant(t, now())); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GrantReset(expired code) error = %v, want not_found", err)
	}

	consumed := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)
	if err := store.OTPChallenges().Consume(t.Context(), consumed.Purpose, consumed.ID, now()); err != nil {
		t.Fatal(err)
	}
	if err := store.OTPChallenges().GrantReset(t.Context(), consumed.ID, newGrant(t, now())); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GrantReset(consumed) error = %v, want not_found", err)
	}
}

// Once granted, only the token spends the challenge: Consume (the one-step path) refuses it, and
// ConsumeResetGrant works once, before the token expires, even after the code itself expired.
func TestResetGrantIsConsumedOnlyWithTheTokenAndOnce(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)
	g := newGrant(t, c.CreatedAt.Add(5*time.Minute)) // verified halfway through the code's life
	if err := store.OTPChallenges().GrantReset(t.Context(), c.ID, g); err != nil {
		t.Fatal(err)
	}

	if err := store.OTPChallenges().Consume(t.Context(), c.Purpose, c.ID, now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Consume(granted) error = %v, want not_found", err)
	}
	if err := store.OTPChallenges().ConsumeResetGrant(t.Context(), c.ID, g.ExpiresAt); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ConsumeResetGrant at the token's expiry error = %v, want not_found", err)
	}
	afterCodeExpiry := c.ExpiresAt.Add(time.Second) // the code is over, the token is not
	if err := store.OTPChallenges().ConsumeResetGrant(t.Context(), c.ID, afterCodeExpiry); err != nil {
		t.Fatalf("ConsumeResetGrant: %v", err)
	}
	if err := store.OTPChallenges().ConsumeResetGrant(t.Context(), c.ID, afterCodeExpiry); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a second ConsumeResetGrant error = %v, want not_found", err)
	}

	ungranted := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)
	if err := store.OTPChallenges().ConsumeResetGrant(t.Context(), ungranted.ID, now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ConsumeResetGrant(no grant) error = %v, want not_found", err)
	}
}

// A new code request invalidates the open challenge, granted or not, so its token dies with it.
func TestResetGrantDiesWithInvalidateOpen(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	c := insertChallenge(t, store, user.ID, domain.OTPPurposePasswordReset)
	if err := store.OTPChallenges().GrantReset(t.Context(), c.ID, newGrant(t, now())); err != nil {
		t.Fatal(err)
	}
	at := now()
	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		return r.OTPChallenges().InvalidateOpen(t.Context(), user.ID, domain.OTPPurposePasswordReset, at)
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.OTPChallenges().ConsumeResetGrant(t.Context(), c.ID, now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ConsumeResetGrant(invalidated) error = %v, want not_found", err)
	}
}

// The CHECK constraints and the unique index hold even against SQL that bypasses the repository.
func TestResetGrantConstraints(t *testing.T) {
	store := newRuntimeStore(t)
	admin := openPool(t, "TEST_ADMIN_DATABASE_URL")
	user := insertUser(t, store)
	verification := insertChallenge(t, store, user.ID, domain.OTPPurposeEmailVerification)
	first := insertChallenge(t, store, user.ID, domain.OTPPurposePasswordReset)
	second := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)
	token := randomHash(t)
	at := now()

	set := `UPDATE app.otp_challenges SET verified_at = $2, reset_token_hash = $3, reset_binding_hash = $4, reset_token_expires_at = $5 WHERE id = $1`
	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{"grant on an email verification challenge", set, []any{verification.ID.String(), at, randomHash(t), randomHash(t), at.Add(time.Minute)}},
		{"half a grant", `UPDATE app.otp_challenges SET verified_at = $2 WHERE id = $1`, []any{first.ID.String(), at}},
		{"token expiring before verification", set, []any{first.ID.String(), at, randomHash(t), randomHash(t), at}},
		{"short token hash", set, []any{first.ID.String(), at, []byte("short"), randomHash(t), at.Add(time.Minute)}},
		{"short binding hash", set, []any{first.ID.String(), at, randomHash(t), []byte("short"), at.Add(time.Minute)}},
	}
	for _, c := range cases {
		if _, err := admin.Exec(t.Context(), c.sql, c.args...); err == nil {
			t.Errorf("%s was accepted", c.name)
		}
	}
	if _, err := admin.Exec(t.Context(), set, first.ID.String(), at, token, randomHash(t), at.Add(time.Minute)); err != nil {
		t.Fatalf("a valid grant: %v", err)
	}
	if _, err := admin.Exec(t.Context(), set, second.ID.String(), at, token, randomHash(t), at.Add(time.Minute)); err == nil {
		t.Error("two challenges with the same token hash were accepted")
	}
}
