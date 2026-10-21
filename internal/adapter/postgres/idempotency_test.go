package postgres

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func newIdempotencyRecord(t *testing.T, scope domain.IdempotencyScope, key string, createdAt time.Time) domain.IdempotencyRecord {
	t.Helper()
	rec, err := domain.NewIdempotencyRecord(domain.IdempotencyRecord{
		Scope: scope, Key: key, RequestHash: randomHash(t),
		Result:    json.RawMessage(`{"id":"t-1","quantity":"0.5","symbol":"AAPL"}`),
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func createScope(t *testing.T, store *Store) domain.IdempotencyScope {
	t.Helper()
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	return domain.IdempotencyScope{UserID: user.ID, PortfolioID: p.ID, Operation: domain.IdempotencyOperationCreateTransaction}
}

func saveRecord(t *testing.T, store *Store, rec domain.IdempotencyRecord) {
	t.Helper()
	if err := store.WithinTransaction(t.Context(), func(r portTx) error { return r.Idempotency().Save(t.Context(), rec) }); err != nil {
		t.Fatalf("save idempotency record: %v", err)
	}
}

func getRecord(t *testing.T, store *Store, scope domain.IdempotencyScope, key string) (domain.IdempotencyRecord, error) {
	t.Helper()
	var got domain.IdempotencyRecord
	err := store.WithinTransaction(t.Context(), func(r portTx) error {
		var err error
		got, err = r.Idempotency().Get(t.Context(), scope, key)
		return err
	})
	return got, err
}

// TestIdempotencySaveAndGet: the stored result is the same JSON value (jsonb may change spacing
// and key order, never the values).
func TestIdempotencySaveAndGet(t *testing.T) {
	store := newRuntimeStore(t)
	rec := newIdempotencyRecord(t, createScope(t, store), "key-1", now())
	saveRecord(t, store, rec)

	got, err := getRecord(t, store, rec.Scope, rec.Key)
	if err != nil || got.Scope != rec.Scope || got.Key != rec.Key || string(got.RequestHash) != string(rec.RequestHash) ||
		!got.CreatedAt.Equal(rec.CreatedAt) || !got.ExpiresAt.Equal(rec.ExpiresAt) {
		t.Fatalf("Get = %+v, %v; want %+v", got, err, rec)
	}
	var want, have any
	if err := json.Unmarshal(rec.Result, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got.Result, &have); err != nil || !reflect.DeepEqual(want, have) {
		t.Fatalf("result = %s, want the same JSON as %s", got.Result, rec.Result)
	}
}

// TestIdempotencyKeysAreScoped: the same key under another user, portfolio or key value is a
// different record.
func TestIdempotencyKeysAreScoped(t *testing.T) {
	store := newRuntimeStore(t)
	rec := newIdempotencyRecord(t, createScope(t, store), "shared-key", now())
	saveRecord(t, store, rec)
	otherScope := createScope(t, store)

	if _, err := getRecord(t, store, otherScope, rec.Key); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(other scope) error = %v, want not_found", err)
	}
	foreignOwner := rec.Scope
	foreignOwner.UserID = otherScope.UserID
	if _, err := getRecord(t, store, foreignOwner, rec.Key); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(another user, same portfolio) error = %v, want not_found", err)
	}
	if _, err := getRecord(t, store, rec.Scope, "other-key"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(other key) error = %v, want not_found", err)
	}
	saveRecord(t, store, newIdempotencyRecord(t, otherScope, rec.Key, now()))
}

// TestIdempotencyConcurrentSaveSucceedsOnce: AGENTS.md "duplicate retries" — the primary key lets
// one of several simultaneous saves of a key win; the rest get idempotency_conflict.
func TestIdempotencyConcurrentSaveSucceedsOnce(t *testing.T) {
	store := newRuntimeStore(t)
	scope := createScope(t, store)

	succeeded, errs := runConcurrently(10, func() error {
		rec := newIdempotencyRecord(t, scope, "retry-key", now())
		return store.WithinTransaction(t.Context(), func(r portTx) error { return r.Idempotency().Save(t.Context(), rec) })
	})

	if succeeded != 1 {
		t.Fatalf("%d concurrent saves succeeded, want exactly 1", succeeded)
	}
	for _, err := range errs {
		if !errors.Is(err, domain.ErrIdempotencyConflict) {
			t.Errorf("losing save error = %v, want idempotency_conflict", err)
		}
	}
}

// TestIdempotencyDeleteExpiredKeepsLiveRecords: a key can be reused only once its record has
// expired; until then DeleteExpired leaves it in place.
func TestIdempotencyDeleteExpiredKeepsLiveRecords(t *testing.T) {
	store := newRuntimeStore(t)
	rec := newIdempotencyRecord(t, createScope(t, store), "ttl-key", now())
	saveRecord(t, store, rec)

	for _, at := range []time.Time{rec.CreatedAt, rec.ExpiresAt.Add(-time.Microsecond)} {
		if err := store.WithinTransaction(t.Context(), func(r portTx) error {
			return r.Idempotency().DeleteExpired(t.Context(), rec.Scope, rec.Key, at)
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := getRecord(t, store, rec.Scope, rec.Key); err != nil {
			t.Fatalf("live record deleted at %v: %v", at, err)
		}
	}
	if err := store.WithinTransaction(t.Context(), func(r portTx) error {
		return r.Idempotency().DeleteExpired(t.Context(), rec.Scope, rec.Key, rec.ExpiresAt)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := getRecord(t, store, rec.Scope, rec.Key); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expired record still there: %v", err)
	}
}

// TestIdempotencyDeleteForPortfolioAllowsPortfolioDelete: records block deleting their portfolio
// (FK RESTRICT) until DeleteForPortfolio removes them; another user cannot remove them.
func TestIdempotencyDeleteForPortfolioAllowsPortfolioDelete(t *testing.T) {
	store := newRuntimeStore(t)
	rec := newIdempotencyRecord(t, createScope(t, store), "delete-key", now())
	saveRecord(t, store, rec)
	stranger := insertUser(t, store)

	if err := store.WithinTransaction(t.Context(), func(r portTx) error {
		return r.Idempotency().DeleteForPortfolio(t.Context(), stranger.ID, rec.Scope.PortfolioID)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := getRecord(t, store, rec.Scope, rec.Key); err != nil {
		t.Fatalf("another user's DeleteForPortfolio removed the record: %v", err)
	}
	err := store.WithinTransaction(t.Context(), func(r portTx) error {
		if err := r.Idempotency().DeleteForPortfolio(t.Context(), rec.Scope.UserID, rec.Scope.PortfolioID); err != nil {
			return err
		}
		return r.Portfolios().DeleteOwnedEmpty(t.Context(), rec.Scope.UserID, rec.Scope.PortfolioID)
	})
	if err != nil {
		t.Fatalf("delete records then portfolio: %v", err)
	}
}
