package postgres

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func insertOIDCFlow(t *testing.T, store *Store, purpose domain.OIDCPurpose, userID domain.ID, createdAt time.Time) domain.OIDCFlow {
	t.Helper()
	f, err := domain.NewOIDCFlow(domain.OIDCFlow{
		ID: newID(t), Purpose: purpose, UserID: userID, StateHash: randomHash(t), NonceHash: randomHash(t),
		BrowserBindingHash: randomHash(t), CreatedAt: createdAt, ExpiresAt: createdAt.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.OIDCFlows().Insert(t.Context(), f); err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	return f
}

func TestOIDCFlowConsumeOnce(t *testing.T) {
	store := newRuntimeStore(t)
	f := insertOIDCFlow(t, store, domain.OIDCPurposeLogin, domain.ID{}, now())
	at := now()

	got, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, at)
	if err != nil || got.ID != f.ID || got.Purpose != domain.OIDCPurposeLogin || !got.UserID.IsZero() ||
		!bytes.Equal(got.NonceHash, f.NonceHash) || got.ConsumedAt == nil || !got.ConsumedAt.Equal(at) {
		t.Fatalf("Consume = %+v, %v", got, err)
	}
	if _, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, at); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second Consume error = %v, want not_found (replayed callback)", err)
	}
}

func TestOIDCFlowConsumeRejectsWrongBindingAndExpiry(t *testing.T) {
	store := newRuntimeStore(t)
	f := insertOIDCFlow(t, store, domain.OIDCPurposeLogin, domain.ID{}, now())

	if _, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, randomHash(t), now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("other browser binding error = %v, want not_found", err)
	}
	if _, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, f.ExpiresAt); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("consume at expiry error = %v, want not_found", err)
	}
	// The failed attempts did not consume it.
	if _, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, now()); err != nil {
		t.Errorf("valid consume after failed attempts: %v", err)
	}
}

func TestOIDCFlowLinkKeepsUser(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	f := insertOIDCFlow(t, store, domain.OIDCPurposeLink, user.ID, now())

	got, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, now())
	if err != nil || got.Purpose != domain.OIDCPurposeLink || got.UserID != user.ID {
		t.Fatalf("Consume(link) = %+v, %v", got, err)
	}
}

func TestOIDCFlowConcurrentConsumeSucceedsOnce(t *testing.T) {
	store := newRuntimeStore(t)
	f := insertOIDCFlow(t, store, domain.OIDCPurposeLogin, domain.ID{}, now())

	succeeded, errs := runConcurrently(10, func() error {
		_, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, now())
		return err
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
