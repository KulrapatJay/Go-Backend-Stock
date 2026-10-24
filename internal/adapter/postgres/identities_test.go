package postgres

import (
	"errors"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func googleLink(t *testing.T, userID domain.ID, subject string) domain.AuthProvider {
	t.Helper()
	link, err := domain.NewAuthProvider(domain.AuthProvider{
		ID: newID(t), UserID: userID, Provider: domain.IdentityProviderGoogle, ProviderSubject: subject, CreatedAt: now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return link
}

func TestIdentityInsertAndRead(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	link := googleLink(t, user.ID, "sub-"+newID(t).String())
	if err := store.Identities().Insert(t.Context(), link); err != nil {
		t.Fatal(err)
	}

	got, err := store.Identities().BySubject(t.Context(), domain.IdentityProviderGoogle, link.ProviderSubject)
	if err != nil || got.ID != link.ID || got.UserID != user.ID || got.Provider != domain.IdentityProviderGoogle ||
		got.ProviderSubject != link.ProviderSubject || !got.CreatedAt.Equal(link.CreatedAt) {
		t.Fatalf("BySubject = %+v, %v; want %+v", got, err, link)
	}
	if _, err := store.Identities().BySubject(t.Context(), domain.IdentityProviderGoogle, "sub-unknown-"+newID(t).String()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("BySubject(unknown) error = %v, want not_found", err)
	}
}

// TestIdentityConflictsKeepTransactionUsable: both unique rules return identity_conflict without
// aborting the transaction, so the service can write auth.identity_link_failed in the same callback.
func TestIdentityConflictsKeepTransactionUsable(t *testing.T) {
	store := newRuntimeStore(t)
	owner := insertUser(t, store)
	subject := "sub-" + newID(t).String()
	if err := store.Identities().Insert(t.Context(), googleLink(t, owner.ID, subject)); err != nil {
		t.Fatal(err)
	}
	intruder := insertUser(t, store)
	e := auditEvent(t, `{}`)

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		// The same Google account cannot link to a second user.
		if err := r.Identities().Insert(t.Context(), googleLink(t, intruder.ID, subject)); !errors.Is(err, domain.ErrIdentityConflict) {
			t.Errorf("same subject error = %v, want identity_conflict", err)
		}
		// A user cannot link a second Google account.
		if err := r.Identities().Insert(t.Context(), googleLink(t, owner.ID, "sub-"+newID(t).String())); !errors.Is(err, domain.ErrIdentityConflict) {
			t.Errorf("second Google account error = %v, want identity_conflict", err)
		}
		return r.Audit().Append(t.Context(), e)
	})

	if err != nil {
		t.Fatalf("transaction after identity conflicts: %v", err)
	}
	if !auditExists(t, e.ID) {
		t.Fatal("audit written after the conflicts was not committed")
	}
}
