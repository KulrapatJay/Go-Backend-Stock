package postgres

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func newChallenge(t *testing.T, userID domain.ID, purpose domain.OTPPurpose, createdAt time.Time) domain.OTPChallenge {
	t.Helper()
	c, err := domain.NewOTPChallenge(domain.OTPChallenge{
		ID: newID(t), UserID: userID, Purpose: purpose, OTPHash: randomHash(t), CreatedAt: createdAt, ExpiresAt: createdAt.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func insertChallenge(t *testing.T, store *Store, userID domain.ID, purpose domain.OTPPurpose) domain.OTPChallenge {
	t.Helper()
	c := newChallenge(t, userID, purpose, now())
	if err := store.OTPChallenges().Insert(t.Context(), c); err != nil {
		t.Fatalf("insert challenge: %v", err)
	}
	return c
}

func TestOTPChallengeInsertAndReadByPurpose(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)

	got, err := store.OTPChallenges().ByID(t.Context(), domain.OTPPurposePasswordReset, c.ID)
	if err != nil || got.ID != c.ID || got.UserID != c.UserID || got.Purpose != c.Purpose || !bytes.Equal(got.OTPHash, c.OTPHash) ||
		!got.ExpiresAt.Equal(c.ExpiresAt) || got.FailedAttempts != 0 || got.ConsumedAt != nil || got.InvalidatedAt != nil {
		t.Fatalf("ByID = %+v, %v; want %+v", got, err, c)
	}
	// The same ID never works for the other flow.
	if _, err := store.OTPChallenges().ByID(t.Context(), domain.OTPPurposeEmailVerification, c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByID(other purpose) error = %v, want not_found", err)
	}
	if err := store.OTPChallenges().Consume(t.Context(), domain.OTPPurposeEmailVerification, c.ID, now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Consume(other purpose) error = %v, want not_found", err)
	}
}

func TestOTPChallengeLockRequiresTransaction(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposeEmailVerification)

	if _, err := store.OTPChallenges().Lock(t.Context(), c.Purpose, c.ID); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("Lock outside a transaction error = %v, want internal", err)
	}
	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		got, err := r.OTPChallenges().Lock(t.Context(), c.Purpose, c.ID)
		if err != nil || got.ID != c.ID {
			t.Errorf("Lock in transaction = %+v, %v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestOTPChallengeInvalidateOpenAllowsANewChallenge: the one-open-challenge index forbids a second
// open challenge, so issuing a new code is InvalidateOpen then Insert (expired ones included).
func TestOTPChallengeInvalidateOpenAllowsANewChallenge(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	expired := newChallenge(t, user.ID, domain.OTPPurposeEmailVerification, now().Add(-time.Hour))
	if err := store.OTPChallenges().Insert(t.Context(), expired); err != nil {
		t.Fatal(err)
	}
	otherFlow := insertChallenge(t, store, user.ID, domain.OTPPurposePasswordReset)
	at := now()

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		if _, err := r.Users().Lock(t.Context(), user.ID); err != nil {
			return err
		}
		if err := r.OTPChallenges().InvalidateOpen(t.Context(), user.ID, domain.OTPPurposeEmailVerification, at); err != nil {
			return err
		}
		return r.OTPChallenges().Insert(t.Context(), newChallenge(t, user.ID, domain.OTPPurposeEmailVerification, at))
	})
	if err != nil {
		t.Fatalf("invalidate then insert: %v", err)
	}

	old, _ := store.OTPChallenges().ByID(t.Context(), domain.OTPPurposeEmailVerification, expired.ID)
	if old.InvalidatedAt == nil || !old.InvalidatedAt.Equal(at) {
		t.Errorf("expired challenge invalidated_at = %v, want %v", old.InvalidatedAt, at)
	}
	untouched, _ := store.OTPChallenges().ByID(t.Context(), domain.OTPPurposePasswordReset, otherFlow.ID)
	if untouched.InvalidatedAt != nil {
		t.Error("InvalidateOpen closed a challenge of the other flow")
	}
}

func TestOTPChallengeRecordFailure(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)

	for range 3 {
		if err := store.OTPChallenges().RecordFailure(t.Context(), c.Purpose, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := store.OTPChallenges().ByID(t.Context(), c.Purpose, c.ID)
	if got.FailedAttempts != 3 {
		t.Errorf("failed_attempts = %d, want 3", got.FailedAttempts)
	}
	if err := store.OTPChallenges().RecordFailure(t.Context(), c.Purpose, newID(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("RecordFailure(unknown) error = %v, want not_found", err)
	}
}

func TestOTPChallengeConsumeOnlyWhileOpenAndUnexpired(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	c := insertChallenge(t, store, user.ID, domain.OTPPurposePasswordReset)

	if err := store.OTPChallenges().Consume(t.Context(), c.Purpose, c.ID, c.ExpiresAt); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("consume at expiry error = %v, want not_found", err)
	}
	at := now()
	if err := store.OTPChallenges().Consume(t.Context(), c.Purpose, c.ID, at); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if err := store.OTPChallenges().Consume(t.Context(), c.Purpose, c.ID, at); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second Consume error = %v, want not_found", err)
	}
	got, _ := store.OTPChallenges().ByID(t.Context(), c.Purpose, c.ID)
	if got.ConsumedAt == nil || !got.ConsumedAt.Equal(at) {
		t.Errorf("consumed_at = %v, want %v", got.ConsumedAt, at)
	}

	invalidated := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)
	if err := store.OTPChallenges().InvalidateOpen(t.Context(), invalidated.UserID, invalidated.Purpose, now()); err != nil {
		t.Fatal(err)
	}
	if err := store.OTPChallenges().Consume(t.Context(), invalidated.Purpose, invalidated.ID, now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Consume(invalidated) error = %v, want not_found", err)
	}
}

// TestOTPChallengeConcurrentConsumeSucceedsOnce: AGENTS.md "concurrent OTP reset" — one code
// resets the password once even when several requests race.
func TestOTPChallengeConcurrentConsumeSucceedsOnce(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)

	succeeded, errs := runConcurrently(10, func() error {
		return store.OTPChallenges().Consume(t.Context(), c.Purpose, c.ID, now())
	})

	if succeeded != 1 {
		t.Fatalf("%d concurrent consumes succeeded, want exactly 1", succeeded)
	}
	for _, err := range errs {
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("losing consume error = %v, want not_found", err)
		}
	}
}

// TestOTPChallengeConcurrentReissueUnderUserLock: resends that race must not trip the
// one-open-challenge index. Locking the user first serializes them, so every resend succeeds and
// exactly one challenge stays open (the lock order milestone 5 services follow).
func TestOTPChallengeConcurrentReissueUnderUserLock(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	issued := make(chan domain.ID, 10)

	succeeded, errs := runConcurrently(10, func() error {
		return store.withinTx(t.Context(), func(r *txRepositories) error {
			if _, err := r.Users().Lock(t.Context(), user.ID); err != nil {
				return err
			}
			at := now()
			if err := r.OTPChallenges().InvalidateOpen(t.Context(), user.ID, domain.OTPPurposeEmailVerification, at); err != nil {
				return err
			}
			c := newChallenge(t, user.ID, domain.OTPPurposeEmailVerification, at)
			if err := r.OTPChallenges().Insert(t.Context(), c); err != nil {
				return err
			}
			issued <- c.ID
			return nil
		})
	})

	if succeeded != 10 {
		t.Fatalf("%d of 10 concurrent resends succeeded, want all; errors: %v", succeeded, errs)
	}
	close(issued)
	open := 0
	for id := range issued {
		c, err := store.OTPChallenges().ByID(t.Context(), domain.OTPPurposeEmailVerification, id)
		if err != nil {
			t.Fatal(err)
		}
		if c.InvalidatedAt == nil && c.ConsumedAt == nil {
			open++
		}
	}
	if open != 1 {
		t.Errorf("%d challenges left open, want exactly 1", open)
	}
}

// TestOTPChallengeRecordFailureSaturatesAndKeepsTransactionUsable: the database allows at most 20
// failed attempts; counting past that must not break the transaction, or the failure audit that
// follows would be lost.
func TestOTPChallengeRecordFailureSaturatesAndKeepsTransactionUsable(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)
	e := auditEvent(t, `{}`)

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		for range 21 {
			if err := r.OTPChallenges().RecordFailure(t.Context(), c.Purpose, c.ID); err != nil {
				return err
			}
		}
		return r.Audit().Append(t.Context(), e)
	})

	if err != nil {
		t.Fatalf("21 failures then an audit in one transaction: %v", err)
	}
	got, _ := store.OTPChallenges().ByID(t.Context(), c.Purpose, c.ID)
	if got.FailedAttempts != 20 {
		t.Errorf("failed_attempts = %d, want 20 (saturated)", got.FailedAttempts)
	}
	if !auditExists(t, e.ID) {
		t.Error("audit written after the failures was not committed")
	}
}
