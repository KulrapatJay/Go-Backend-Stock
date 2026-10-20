package postgres

import (
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestUserInsertAndRead(t *testing.T) {
	store := newRuntimeStore(t)
	u := insertUser(t, store)

	byID, err := store.Users().ByID(t.Context(), u.ID)
	if err != nil || byID.ID != u.ID || byID.Email != u.Email || byID.EmailVerifiedAt != nil ||
		!byID.CreatedAt.Equal(u.CreatedAt) || !byID.UpdatedAt.Equal(u.UpdatedAt) {
		t.Fatalf("ByID = %+v, %v; want %+v", byID, err, u)
	}
	byEmail, err := store.Users().ByEmail(t.Context(), u.Email)
	if err != nil || byEmail.ID != u.ID {
		t.Fatalf("ByEmail = %+v, %v", byEmail, err)
	}
	if _, err := store.Users().ByID(t.Context(), newID(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByID(unknown) error = %v, want not_found", err)
	}
	if _, err := store.Users().ByEmail(t.Context(), uniqueEmail(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByEmail(unknown) error = %v, want not_found", err)
	}
}

// TestUserEmailIsComparedExactly: the stored value is Go's normalized email, compared without
// lower() in SQL, so non-ASCII addresses behave the same in Go and in the database.
func TestUserEmailIsComparedExactly(t *testing.T) {
	store := newRuntimeStore(t)
	e, err := domain.ParseEmail("ÄLICE-" + newID(t).String() + "@EXAMPLE.COM")
	if err != nil {
		t.Fatal(err)
	}
	at := now()
	u := domain.User{ID: newID(t), Email: e, CreatedAt: at, UpdatedAt: at}
	if err := store.Users().Insert(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	got, err := store.Users().ByEmail(t.Context(), e)
	if err != nil || got.ID != u.ID || got.Email.String() != e.String() {
		t.Fatalf("ByEmail(%q) = %+v, %v", e, got, err)
	}
}

// TestUserInsertDuplicateEmailKeepsTransactionUsable is the review finding of plan 3a: a conflict
// must not abort the transaction, so the caller can still write in the same callback.
func TestUserInsertDuplicateEmailKeepsTransactionUsable(t *testing.T) {
	store := newRuntimeStore(t)
	first := insertUser(t, store)
	second := domain.User{ID: newID(t), Email: uniqueEmail(t), CreatedAt: now(), UpdatedAt: now()}

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		dup := domain.User{ID: newID(t), Email: first.Email, CreatedAt: now(), UpdatedAt: now()}
		if err := r.Users().Insert(t.Context(), dup); !errors.Is(err, domain.ErrIdentityConflict) {
			t.Errorf("duplicate email error = %v, want identity_conflict", err)
		}
		return r.Users().Insert(t.Context(), second) // must still work in the same transaction
	})

	if err != nil {
		t.Fatalf("transaction after a conflict: %v", err)
	}
	if _, err := store.Users().ByID(t.Context(), second.ID); err != nil {
		t.Fatalf("write after the conflict was not committed: %v", err)
	}
}

func TestUserCredentialsAndPassword(t *testing.T) {
	store := newRuntimeStore(t)
	u := insertUser(t, store)

	creds, err := store.Users().CredentialsByEmail(t.Context(), u.Email)
	if err != nil || creds.User.ID != u.ID || creds.HasPassword() {
		t.Fatalf("credentials before SetPassword = %+v, %v; want no password (Google-only shape)", creds.User, err)
	}

	const hash = domain.PasswordHash("$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA")
	at := now().Add(time.Second)
	if err := store.Users().SetPassword(t.Context(), u.ID, hash, at); err != nil {
		t.Fatal(err)
	}
	creds, err = store.Users().CredentialsByEmail(t.Context(), u.Email)
	if err != nil || creds.PasswordHash != hash || !creds.User.UpdatedAt.Equal(at) {
		t.Fatalf("credentials after SetPassword: hash matches=%v updated=%v err=%v", creds.PasswordHash == hash, creds.User.UpdatedAt, err)
	}

	if err := store.Users().SetPassword(t.Context(), newID(t), hash, at); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetPassword(unknown) error = %v, want not_found", err)
	}
	if err := store.Users().SetPassword(t.Context(), u.ID, "", at); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("SetPassword(empty hash) error = %v, want internal", err)
	}
}

func TestUserMarkEmailVerifiedKeepsFirstTime(t *testing.T) {
	store := newRuntimeStore(t)
	u := insertUser(t, store)
	first := now().Add(time.Minute)

	if err := store.Users().MarkEmailVerified(t.Context(), u.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Users().MarkEmailVerified(t.Context(), u.ID, first.Add(time.Hour)); err != nil {
		t.Fatalf("second MarkEmailVerified must be a no-op, got %v", err)
	}
	got, err := store.Users().ByID(t.Context(), u.ID)
	if err != nil || got.EmailVerifiedAt == nil || !got.EmailVerifiedAt.Equal(first) || !got.UpdatedAt.Equal(first) {
		t.Fatalf("verified_at = %v updated_at = %v err %v; want both %v", got.EmailVerifiedAt, got.UpdatedAt, err, first)
	}
	if err := store.Users().MarkEmailVerified(t.Context(), newID(t), first); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("MarkEmailVerified(unknown) error = %v, want not_found", err)
	}
}

func TestUserLockRequiresTransaction(t *testing.T) {
	store := newRuntimeStore(t)
	u := insertUser(t, store)

	if _, err := store.Users().Lock(t.Context(), u.ID); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("Lock outside a transaction error = %v, want internal", err)
	}
	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		got, err := r.Users().Lock(t.Context(), u.ID)
		if err != nil || got.ID != u.ID {
			t.Errorf("Lock in transaction = %+v, %v", got, err)
		}
		if _, err := r.Users().Lock(t.Context(), newID(t)); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Lock(unknown) error = %v, want not_found", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
