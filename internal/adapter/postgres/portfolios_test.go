package postgres

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestPortfolioInsertAndGetOwned(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := newPortfolio(t, user.ID, "Long term", now())
	desc, err := domain.ParsePortfolioDescription("Buy and hold.\r\nReview yearly.")
	if err != nil {
		t.Fatal(err)
	}
	p.Description = desc
	if err := store.Portfolios().Insert(t.Context(), p); err != nil {
		t.Fatal(err)
	}

	got, err := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID)
	if err != nil || got.ID != p.ID || got.UserID != user.ID || got.Name.String() != "Long term" ||
		got.Description.String() != desc.String() || got.Currency != domain.CurrencyUSD || got.Version.Int32() != 1 ||
		!got.CreatedAt.Equal(p.CreatedAt) || !got.UpdatedAt.Equal(p.CreatedAt) {
		t.Fatalf("GetOwned = %+v, %v; want %+v at version 1", got, err, p)
	}
	if _, err := store.Portfolios().GetOwned(t.Context(), user.ID, newID(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetOwned(unknown) error = %v, want not_found", err)
	}
}

// TestPortfolioNameIsUniquePerOwnerIgnoringCase: "Long term" and "long TERM" collide for one
// owner, the conflict keeps the transaction usable, another owner may use the name, and a deleted
// portfolio frees its name.
func TestPortfolioNameIsUniquePerOwnerIgnoringCase(t *testing.T) {
	store := newRuntimeStore(t)
	owner := insertUser(t, store)
	first := newPortfolio(t, owner.ID, "Long term", now())
	if err := store.Portfolios().Insert(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	e := auditEvent(t, `{}`)

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		if err := r.Portfolios().Insert(t.Context(), newPortfolio(t, owner.ID, "long TERM", now())); !errors.Is(err, domain.ErrPortfolioNameConflict) {
			t.Errorf("same name in another case error = %v, want portfolio_name_conflict", err)
		}
		return r.Audit().Append(t.Context(), e)
	})
	if err != nil || !auditExists(t, e.ID) {
		t.Fatalf("transaction after the name conflict: %v (audit committed: %v)", err, auditExists(t, e.ID))
	}

	other := insertUser(t, store)
	if err := store.Portfolios().Insert(t.Context(), newPortfolio(t, other.ID, "LONG TERM", now())); err != nil {
		t.Errorf("another owner using the name: %v", err)
	}
	if err := store.Portfolios().DeleteOwnedEmpty(t.Context(), owner.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Portfolios().Insert(t.Context(), newPortfolio(t, owner.ID, "long term", now())); err != nil {
		t.Errorf("reusing the name of a deleted portfolio: %v", err)
	}
}

// TestPortfolioOwnershipIsolation: every method of another user's portfolio reads as not found
// and changes nothing.
func TestPortfolioOwnershipIsolation(t *testing.T) {
	store := newRuntimeStore(t)
	alice := insertUser(t, store)
	bob := insertUser(t, store)
	p := insertPortfolio(t, store, alice.ID)

	if _, err := store.Portfolios().GetOwned(t.Context(), bob.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetOwned error = %v, want not_found", err)
	}
	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		_, err := r.Portfolios().LockOwned(t.Context(), bob.ID, p.ID)
		return err
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("LockOwned error = %v, want not_found", err)
	}
	renamed := p
	renamed.Name, _ = domain.ParsePortfolioName("Taken over")
	renamed.UpdatedAt = now()
	if _, err := store.Portfolios().UpdateOwned(t.Context(), renamed, bob.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateOwned error = %v, want not_found", err)
	}
	if err := store.Portfolios().DeleteOwnedEmpty(t.Context(), bob.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("DeleteOwnedEmpty error = %v, want not_found", err)
	}
	list, err := store.Portfolios().ListOwned(t.Context(), bob.ID, page(t, 100, ""))
	if err != nil || len(list.Items) != 0 {
		t.Errorf("bob's list = %+v, %v; want empty", list, err)
	}
	if got, err := store.Portfolios().GetOwned(t.Context(), alice.ID, p.ID); err != nil || got.Name != p.Name || got.Version.Int32() != 1 {
		t.Errorf("alice's portfolio changed: %+v, %v", got, err)
	}
}

func TestPortfolioLockOwnedRequiresTransaction(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)

	if _, err := store.Portfolios().LockOwned(t.Context(), user.ID, p.ID); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("LockOwned outside a transaction error = %v, want internal", err)
	}
	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		got, err := r.Portfolios().LockOwned(t.Context(), user.ID, p.ID)
		if err == nil && got.ID != p.ID {
			t.Errorf("LockOwned = %+v", got)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPortfolioUpdateOwnedChecksVersion(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	edit := p
	edit.Name, _ = domain.ParsePortfolioName("Short term")
	edit.Description, _ = domain.ParsePortfolioDescription("Swing trades")
	edit.UpdatedAt = now().Add(time.Minute)

	got, err := store.Portfolios().UpdateOwned(t.Context(), edit, user.ID)
	if err != nil || got.Version.Int32() != 2 || got.Name.String() != "Short term" || got.Description.String() != "Swing trades" ||
		!got.UpdatedAt.Equal(edit.UpdatedAt) || !got.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("UpdateOwned = %+v, %v; want the edit at version 2", got, err)
	}

	stale := edit
	stale.Name, _ = domain.ParsePortfolioName("Lost update")
	if _, err := store.Portfolios().UpdateOwned(t.Context(), stale, user.ID); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("UpdateOwned(version 1 again) error = %v, want version_conflict", err)
	}
	if stored, _ := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); stored.Name.String() != "Short term" || stored.Version.Int32() != 2 {
		t.Errorf("stale update changed the row: %+v", stored)
	}
}

// TestPortfolioRenameToTakenNameConflicts: the rename conflict aborts the transaction, which is
// fine because the service returns at once (port.UnitOfWork).
func TestPortfolioRenameToTakenNameConflicts(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	taken := insertPortfolio(t, store, user.ID)
	p := insertPortfolio(t, store, user.ID)
	edit := p
	edit.Name, _ = domain.ParsePortfolioName(taken.Name.String())
	edit.UpdatedAt = now()

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		_, err := r.Portfolios().UpdateOwned(t.Context(), edit, user.ID)
		return err
	})

	if !errors.Is(err, domain.ErrPortfolioNameConflict) {
		t.Fatalf("rename to a taken name error = %v, want portfolio_name_conflict", err)
	}
	if got, _ := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); got.Name != p.Name {
		t.Errorf("name changed to %q", got.Name)
	}
}

// TestPortfolioListOwnedPagesWithoutRepeats: equal created_at values are ordered by id, so
// walking every page returns each portfolio exactly once, newest first.
func TestPortfolioListOwnedPagesWithoutRepeats(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	base := now()
	want := map[domain.ID]bool{}
	for i := range 7 {
		at := base.Add(time.Duration(i/3) * time.Second) // three share each second
		p := newPortfolio(t, user.ID, "Page "+newID(t).String(), at)
		if err := store.Portfolios().Insert(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		want[p.ID] = true
	}

	var got []domain.Portfolio
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("pagination does not end")
		}
		res, err := store.Portfolios().ListOwned(t.Context(), user.ID, page(t, 2, cursor))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, res.Items...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}

	if len(got) != len(want) {
		t.Fatalf("listed %d portfolios, want %d", len(got), len(want))
	}
	for i, p := range got {
		if !want[p.ID] {
			t.Fatalf("portfolio %s listed twice or unknown", p.ID)
		}
		delete(want, p.ID)
		if i > 0 {
			prev := got[i-1]
			if p.CreatedAt.After(prev.CreatedAt) || (p.CreatedAt.Equal(prev.CreatedAt) && p.ID.String() > prev.ID.String()) {
				t.Errorf("item %d is out of order (created_at DESC, id DESC)", i)
			}
		}
	}
}

func TestPortfolioListOwnedRejectsForeignCursors(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	transactionsCursor := encodeCursor(cursorPosition{Kind: cursorKindTransactions, At: now(), Sequence: 1})
	for name, cursor := range map[string]string{
		"not base64":          "%%%",
		"not json":            "bm90IGpzb24",
		"transactions cursor": transactionsCursor,
		"bad id":              encodeCursor(cursorPosition{Kind: cursorKindPortfolios, At: now(), ID: "nope"}),
	} {
		_, err := store.Portfolios().ListOwned(t.Context(), user.ID, page(t, 10, cursor))
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != domain.CodeInvalidInput || de.Message[:6] != "cursor" {
			t.Errorf("%s: error = %v, want invalid_input about the cursor", name, err)
		}
	}
}

func page(t *testing.T, limit int, cursor string) domain.Page {
	t.Helper()
	p, err := domain.ParsePage(strconv.Itoa(limit), cursor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newPortfolio(t *testing.T, userID domain.ID, name string, createdAt time.Time) domain.Portfolio {
	t.Helper()
	n, err := domain.ParsePortfolioName(name)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Portfolio{ID: newID(t), UserID: userID, Name: n, Currency: domain.CurrencyUSD, CreatedAt: createdAt, UpdatedAt: createdAt}
}

// insertPortfolio stores a portfolio with a unique name for userID.
func insertPortfolio(t *testing.T, store *Store, userID domain.ID) domain.Portfolio {
	t.Helper()
	p := newPortfolio(t, userID, "Portfolio "+newID(t).String(), now())
	if err := store.Portfolios().Insert(t.Context(), p); err != nil {
		t.Fatalf("insert portfolio: %v", err)
	}
	p.Version = mustVersion(t, 1)
	return p
}

func mustVersion(t *testing.T, n int64) domain.Version {
	t.Helper()
	v, err := domain.ParseVersion(n)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestPortfolioListRejectsCursorFromAnotherOwner: a portfolio cursor continues the list of the
// owner it was issued for.
func TestPortfolioListRejectsCursorFromAnotherOwner(t *testing.T) {
	store := newRuntimeStore(t)
	alice, bob := insertUser(t, store), insertUser(t, store)
	for range 3 {
		insertPortfolio(t, store, alice.ID)
		insertPortfolio(t, store, bob.ID)
	}
	first, err := store.Portfolios().ListOwned(t.Context(), alice.ID, page(t, 1, ""))
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page of alice = %+v, %v", first, err)
	}

	_, err = store.Portfolios().ListOwned(t.Context(), bob.ID, page(t, 1, first.NextCursor))

	requireCode(t, err, domain.CodeInvalidInput)
}
