package postgres

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func insertSession(t *testing.T, store *Store, userID domain.ID) domain.Session {
	t.Helper()
	at := now()
	s, err := domain.NewSession(domain.Session{ID: newID(t), UserID: userID, TokenHash: randomHash(t), CreatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions().Insert(t.Context(), s); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return s
}

func TestSessionInsertAndRead(t *testing.T) {
	store := newRuntimeStore(t)
	s := insertSession(t, store, insertUser(t, store).ID)

	got, err := store.Sessions().ByTokenHash(t.Context(), s.TokenHash)
	if err != nil || got.ID != s.ID || got.UserID != s.UserID || !bytes.Equal(got.TokenHash, s.TokenHash) ||
		!got.CreatedAt.Equal(s.CreatedAt) || !got.ExpiresAt.Equal(s.ExpiresAt) || got.RevokedAt != nil || got.RevokeReason != "" {
		t.Fatalf("ByTokenHash = %+v, %v; want %+v", got, err, s)
	}
	if _, err := store.Sessions().ByTokenHash(t.Context(), randomHash(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByTokenHash(unknown) error = %v, want not_found", err)
	}
}

func TestSessionRevokeReportsOnlyTheFirstRevoke(t *testing.T) {
	store := newRuntimeStore(t)
	s := insertSession(t, store, insertUser(t, store).ID)
	at := now()

	first, err := store.Sessions().Revoke(t.Context(), s.ID, at, domain.RevokeReasonLogout)
	if err != nil || !first {
		t.Fatalf("first Revoke = %v, %v; want true", first, err)
	}
	second, err := store.Sessions().Revoke(t.Context(), s.ID, at.Add(time.Minute), domain.RevokeReasonLogout)
	if err != nil || second {
		t.Fatalf("second Revoke = %v, %v; want false so logout audits once", second, err)
	}
	unknown, err := store.Sessions().Revoke(t.Context(), newID(t), at, domain.RevokeReasonLogout)
	if err != nil || unknown {
		t.Errorf("Revoke(unknown) = %v, %v; want false", unknown, err)
	}

	got, err := store.Sessions().ByTokenHash(t.Context(), s.TokenHash)
	if err != nil || got.RevokedAt == nil || !got.RevokedAt.Equal(at) || got.RevokeReason != domain.RevokeReasonLogout {
		t.Fatalf("revoked session = %+v, %v; want revoked at the first time with reason logout", got, err)
	}
}

func TestSessionRevokeAllRevokesOnlyTheUsersActiveSessions(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	other := insertUser(t, store)
	a := insertSession(t, store, user.ID)
	b := insertSession(t, store, user.ID)
	alreadyRevoked := insertSession(t, store, user.ID)
	othersSession := insertSession(t, store, other.ID)
	if _, err := store.Sessions().Revoke(t.Context(), alreadyRevoked.ID, now(), domain.RevokeReasonLogout); err != nil {
		t.Fatal(err)
	}

	n, err := store.Sessions().RevokeAll(t.Context(), user.ID, now(), domain.RevokeReasonPasswordReset)
	if err != nil || n != 2 {
		t.Fatalf("RevokeAll = %d, %v; want 2", n, err)
	}
	for _, s := range []domain.Session{a, b} {
		got, _ := store.Sessions().ByTokenHash(t.Context(), s.TokenHash)
		if got.RevokeReason != domain.RevokeReasonPasswordReset {
			t.Errorf("session %s reason = %q, want password_reset", s.ID, got.RevokeReason)
		}
	}
	if got, _ := store.Sessions().ByTokenHash(t.Context(), alreadyRevoked.TokenHash); got.RevokeReason != domain.RevokeReasonLogout {
		t.Errorf("already revoked session changed reason to %q", got.RevokeReason)
	}
	if got, _ := store.Sessions().ByTokenHash(t.Context(), othersSession.TokenHash); got.RevokedAt != nil {
		t.Error("RevokeAll revoked another user's session")
	}
}
