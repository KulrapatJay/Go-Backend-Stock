package postgres

import (
	"encoding/json"
	"errors"
	"testing"

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

	succeeded, errs := runConcurrently(10, func() error {
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
			_, err = r.Transactions().Insert(t.Context(), user.ID, newTrade(t, p.ID, instrument, domain.SideSell, "1", "11", now()))
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
