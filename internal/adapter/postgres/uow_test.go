package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

type portTx = port.TxRepositories

// TestWithinTransactionRollsBackPortfolioWhenAuditFails is the failure injection of milestone 3
// spec section 4.5: a business change and its audit commit together or not at all.
func TestWithinTransactionRollsBackPortfolioWhenAuditFails(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := newPortfolio(t, user.ID, "Rolled back "+newID(t).String(), now())
	bad := auditEvent(t, `{}`)
	bad.Metadata = json.RawMessage(`["not","an","object"]`) // bypasses NewAuditEvent; violates audit_events_metadata_object

	err := store.WithinTransaction(t.Context(), func(r portTx) error {
		if err := r.Portfolios().Insert(t.Context(), p); err != nil {
			return err
		}
		return r.Audit().Append(t.Context(), bad)
	})

	if !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("error = %v, want internal from the audit CHECK", err)
	}
	if _, err := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("portfolio committed although its audit failed: %v", err)
	}
}

func TestWithinTransactionCommitsPortfolioWithAudit(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := newPortfolio(t, user.ID, "Committed "+newID(t).String(), now())
	e := auditEvent(t, `{}`)

	if err := store.WithinTransaction(t.Context(), func(r portTx) error {
		if err := r.Portfolios().Insert(t.Context(), p); err != nil {
			return err
		}
		return r.Audit().Append(t.Context(), e)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); err != nil || !auditExists(t, e.ID) {
		t.Fatalf("portfolio or audit missing after commit: %v", err)
	}
}

var errNotEnoughShares = errors.New("not enough shares")

// TestConcurrentSellsNeverOversell: AGENTS.md "concurrent sells". Ten requests each sell one of
// five held shares; LockOwned serializes them, so exactly five succeed and the holding never goes
// negative. (Counting shares here stands in for the replay of milestone 7.)
func TestConcurrentSellsNeverOversell(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "5", "10", now()))
	one := decimal.NewFromInt(1)
	sells := make([]domain.Transaction, 10)
	for i := range sells {
		sells[i] = newTrade(t, p.ID, instrument, domain.SideSell, "1", "11", now())
	}
	next := prepared(sells)

	succeeded, errs := runConcurrently(10, func() error {
		sell := next()
		return store.WithinTransaction(t.Context(), func(r portTx) error {
			if _, err := r.Portfolios().LockOwned(t.Context(), user.ID, p.ID); err != nil {
				return err
			}
			history, err := r.Transactions().History(t.Context(), user.ID, p.ID)
			if err != nil {
				return err
			}
			held := decimal.Zero
			for _, tr := range history {
				if tr.Side == domain.SideBuy {
					held = held.Add(tr.Quantity.Decimal())
				} else {
					held = held.Sub(tr.Quantity.Decimal())
				}
			}
			if held.LessThan(one) {
				return errNotEnoughShares
			}
			_, err = r.Transactions().Insert(t.Context(), user.ID, sell)
			return err
		})
	})

	if succeeded != 5 {
		t.Fatalf("%d of 10 concurrent sells succeeded, want exactly 5", succeeded)
	}
	for _, err := range errs {
		if !errors.Is(err, errNotEnoughShares) {
			t.Errorf("rejected sell error = %v, want not enough shares", err)
		}
	}
}

// newBoundedStore returns a Store over a pool from NewPool, so it carries the lock and statement
// timeouts of production. newRuntimeStore uses a bare pgxpool and does not.
func newBoundedStore(t *testing.T) *Store {
	t.Helper()
	pool, err := NewPool(t.Context(), integrationURL(t, "TEST_RUNTIME_DATABASE_URL"), 4)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool)
}

func requireServerBusy(t *testing.T, err error) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != domain.CodeServerBusy || de.RetryAfter != time.Second {
		t.Fatalf("error = %v, want server_busy with RetryAfter 1s", err)
	}
}

// TestLockWaitEndsAsServerBusy: a request that waits for a portfolio lock must give up (lock_timeout
// 3s) with a retryable error instead of holding a pooled connection for as long as the holder lives.
func TestLockWaitEndsAsServerBusy(t *testing.T) {
	store := newBoundedStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)

	locked, release, holderDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		holderDone <- store.WithinTransaction(context.Background(), func(r portTx) error {
			if _, err := r.Portfolios().LockOwned(context.Background(), user.ID, p.ID); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case err := <-holderDone:
		t.Fatalf("holder ended before locking: %v", err)
	}

	start := time.Now()
	err := store.WithinTransaction(t.Context(), func(r portTx) error {
		_, err := r.Portfolios().LockOwned(t.Context(), user.ID, p.ID)
		return err
	})
	waited := time.Since(start)
	close(release)
	if holdErr := <-holderDone; holdErr != nil {
		t.Fatalf("holder: %v", holdErr)
	}

	requireServerBusy(t, err)
	if waited > 4*time.Second {
		t.Errorf("waiter gave up after %v, want within 4s", waited)
	}
	if err := store.WithinTransaction(t.Context(), func(r portTx) error {
		_, err := r.Portfolios().LockOwned(t.Context(), user.ID, p.ID)
		return err
	}); err != nil {
		t.Fatalf("lock after the holder finished: %v", err)
	}
}

// TestDeadlockEndsAsServerBusy: two transactions lock portfolios A and B in opposite order.
// PostgreSQL aborts one with 40P01, which must be a retryable 503 and not a 500; the other commits.
func TestDeadlockEndsAsServerBusy(t *testing.T) {
	store := newBoundedStore(t)
	user := insertUser(t, store)
	a, b := insertPortfolio(t, store, user.ID), insertPortfolio(t, store, user.ID)

	var firstLocked [2]chan struct{}
	for i := range firstLocked {
		firstLocked[i] = make(chan struct{})
	}
	run := func(self int, first, second domain.ID) error {
		return store.WithinTransaction(context.Background(), func(r portTx) error {
			if _, err := r.Portfolios().LockOwned(context.Background(), user.ID, first); err != nil {
				return err
			}
			close(firstLocked[self])
			<-firstLocked[1-self] // both hold their first lock before either asks for the second
			_, err := r.Portfolios().LockOwned(context.Background(), user.ID, second)
			return err
		})
	}
	results := make(chan error, 2)
	go func() { results <- run(0, a.ID, b.ID) }()
	go func() { results <- run(1, b.ID, a.ID) }()

	var busy, committed int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			committed++
		case errors.Is(err, errDatabaseBusy):
			requireServerBusy(t, err)
			busy++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if busy != 1 || committed != 1 {
		t.Fatalf("busy = %d, committed = %d, want one of each", busy, committed)
	}
}

func TestTransactionPastItsDeadlineIsServerBusy(t *testing.T) {
	store := newBoundedStore(t)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	err := store.WithinTransaction(ctx, func(portTx) error { return nil })

	requireServerBusy(t, err)
}
