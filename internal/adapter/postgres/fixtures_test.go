package postgres

import (
	"crypto/rand"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// now returns the current time at microsecond precision, which is what PostgreSQL stores, so
// values read back compare equal.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

func randomHash(t *testing.T) []byte {
	t.Helper()
	h := make([]byte, domain.HashSize)
	if _, err := rand.Read(h); err != nil {
		t.Fatal(err)
	}
	return h
}

// uniqueEmail returns an address no other test uses, already normalized.
func uniqueEmail(t *testing.T) domain.Email {
	t.Helper()
	e, err := domain.ParseEmail("user-" + newID(t).String() + "@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// insertUser stores a fresh unverified account through the repository and returns it.
func insertUser(t *testing.T, store *Store) domain.User {
	t.Helper()
	at := now()
	u := domain.User{ID: newID(t), Email: uniqueEmail(t), CreatedAt: at, UpdatedAt: at}
	if err := store.Users().Insert(t.Context(), u); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return u
}
