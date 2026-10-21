package postgres

import (
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// TestTransactionInsertKeepsEveryDigit: numeric(28,10) round-trips the smallest and the largest
// values the API accepts, and the database assigns a sequence.
func TestTransactionInsertKeepsEveryDigit(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	trade := newTrade(t, p.ID, instrument, domain.SideBuy, "123456789012345678.0123456789", "0.0000000001", now())

	got := insertTrade(t, store, user.ID, trade)

	if got.Sequence < 1 || got.Symbol != instrument.Symbol || got.Version.Int32() != 1 ||
		got.Quantity.String() != "123456789012345678.0123456789" || got.Price.String() != "0.0000000001" ||
		got.Fee.String() != "1.25" || got.Side != domain.SideBuy || !got.ExecutedAt.Equal(trade.ExecutedAt) {
		t.Fatalf("Insert = %+v", got)
	}
	again, err := store.Transactions().Get(t.Context(), user.ID, p.ID, got.ID)
	if err != nil || again.Quantity.String() != got.Quantity.String() || again.Price.String() != got.Price.String() || again.Sequence != got.Sequence {
		t.Fatalf("Get = %+v, %v; want %+v", again, err, got)
	}
}

// TestTransactionOwnershipIsolation: another user gets not_found from every method on Alice's
// portfolio and trade, and nothing changes.
func TestTransactionOwnershipIsolation(t *testing.T) {
	store := newRuntimeStore(t)
	alice := insertUser(t, store)
	bob := insertUser(t, store)
	p := insertPortfolio(t, store, alice.ID)
	instrument := insertInstrument(t, store)
	trade := insertTrade(t, store, alice.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "2", "10", now()))

	if _, err := store.Transactions().History(t.Context(), bob.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("History error = %v, want not_found", err)
	}
	filter, _ := domain.NewTransactionFilter(page(t, 10, ""), nil, nil, nil, nil)
	if _, err := store.Transactions().List(t.Context(), bob.ID, p.ID, filter); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("List error = %v, want not_found", err)
	}
	if _, err := store.Transactions().Get(t.Context(), bob.ID, p.ID, trade.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get error = %v, want not_found", err)
	}
	if _, err := store.Transactions().Insert(t.Context(), bob.ID, newTrade(t, p.ID, instrument, domain.SideSell, "2", "10", now())); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Insert error = %v, want not_found", err)
	}
	edit := trade
	edit.Quantity, _ = domain.ParseQuantity("999")
	edit.UpdatedAt = now()
	if _, err := store.Transactions().Update(t.Context(), bob.ID, edit); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update error = %v, want not_found", err)
	}
	if err := store.Transactions().Delete(t.Context(), bob.ID, p.ID, trade.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete error = %v, want not_found", err)
	}
	history, err := store.Transactions().History(t.Context(), alice.ID, p.ID)
	if err != nil || len(history) != 1 || history[0].Quantity.String() != "2" || history[0].Version.Int32() != 1 {
		t.Errorf("alice's history changed: %+v, %v", history, err)
	}
}

func TestTransactionHistoryOfEmptyOwnedPortfolio(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)

	history, err := store.Transactions().History(t.Context(), user.ID, p.ID)
	if err != nil || len(history) != 0 {
		t.Fatalf("History(empty portfolio) = %+v, %v; want empty, no error", history, err)
	}
}

// TestTransactionSameSymbolInTwoPortfoliosStaysSeparate: trades of one instrument in two
// portfolios of the same user never mix.
func TestTransactionSameSymbolInTwoPortfoliosStaysSeparate(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	long := insertPortfolio(t, store, user.ID)
	short := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	insertTrade(t, store, user.ID, newTrade(t, long.ID, instrument, domain.SideBuy, "10", "100", now()))
	insertTrade(t, store, user.ID, newTrade(t, short.ID, instrument, domain.SideBuy, "1", "100", now()))

	for _, tc := range []struct {
		p    domain.Portfolio
		want string
	}{{long, "10"}, {short, "1"}} {
		history, err := store.Transactions().History(t.Context(), user.ID, tc.p.ID)
		if err != nil || len(history) != 1 || history[0].Quantity.String() != tc.want || history[0].PortfolioID != tc.p.ID {
			t.Errorf("history of %s = %+v, %v; want one trade of %s", tc.p.ID, history, err, tc.want)
		}
	}
}

// TestTransactionHistoryOrdersByExecutedAtThenSequence: a backdated trade replays before later
// ones, and trades at the same instant replay in insertion (sequence) order.
func TestTransactionHistoryOrdersByExecutedAtThenSequence(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	at := now().Add(-time.Hour)
	first := insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "1", "10", at))
	second := insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "2", "10", at))
	backdated := insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "3", "10", at.Add(-time.Minute)))

	history, err := store.Transactions().History(t.Context(), user.ID, p.ID)
	if err != nil || len(history) != 3 {
		t.Fatalf("History = %+v, %v", history, err)
	}
	for i, want := range []domain.ID{backdated.ID, first.ID, second.ID} {
		if history[i].ID != want {
			t.Errorf("history[%d] = %s, want %s (executed_at, then sequence)", i, history[i].ID, want)
		}
	}
}

func TestTransactionUpdateChecksVersionAndNeverMovesPortfolio(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	other := insertPortfolio(t, store, user.ID)
	trade := insertTrade(t, store, user.ID, newTrade(t, p.ID, insertInstrument(t, store), domain.SideBuy, "1", "10", now()))
	replacement := insertInstrument(t, store)

	edit := trade
	edit.InstrumentID = replacement.ID
	edit.Side = domain.SideSell
	edit.Quantity, _ = domain.ParseQuantity("0.5")
	edit.ExecutedAt = trade.ExecutedAt.Add(-time.Hour)
	edit.UpdatedAt = now().Add(time.Minute)
	got, err := store.Transactions().Update(t.Context(), user.ID, edit)
	if err != nil || got.Version.Int32() != 2 || got.Symbol != replacement.Symbol || got.Side != domain.SideSell ||
		got.Quantity.String() != "0.5" || !got.ExecutedAt.Equal(edit.ExecutedAt) || got.Sequence != trade.Sequence {
		t.Fatalf("Update = %+v, %v; want the edit at version 2 with the same sequence", got, err)
	}

	if _, err := store.Transactions().Update(t.Context(), user.ID, edit); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("Update(stale version) error = %v, want version_conflict", err)
	}
	moved := got
	moved.PortfolioID = other.ID
	if _, err := store.Transactions().Update(t.Context(), user.ID, moved); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update into another portfolio error = %v, want not_found", err)
	}
	stored, err := store.Transactions().Get(t.Context(), user.ID, p.ID, trade.ID)
	if err != nil || stored.Version.Int32() != 2 || stored.PortfolioID != p.ID {
		t.Errorf("trade after rejected updates = %+v, %v", stored, err)
	}
}

func TestTransactionDelete(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	trade := insertTrade(t, store, user.ID, newTrade(t, p.ID, insertInstrument(t, store), domain.SideBuy, "1", "10", now()))

	if err := store.Transactions().Delete(t.Context(), user.ID, p.ID, trade.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transactions().Get(t.Context(), user.ID, p.ID, trade.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after Delete error = %v, want not_found", err)
	}
	if err := store.Transactions().Delete(t.Context(), user.ID, p.ID, trade.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second Delete error = %v, want not_found", err)
	}
}

// listAll walks every page of a filtered list and returns the trades in order.
func listAll(t *testing.T, store *Store, ownerID, portfolioID domain.ID, limit int, symbol *domain.Symbol, side *domain.Side, from, to *time.Time) []domain.Transaction {
	t.Helper()
	var out []domain.Transaction
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 20 {
			t.Fatal("pagination does not end")
		}
		f, err := domain.NewTransactionFilter(page(t, limit, cursor), symbol, side, from, to)
		if err != nil {
			t.Fatal(err)
		}
		res, err := store.Transactions().List(t.Context(), ownerID, portfolioID, f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, res.Items...)
		if res.NextCursor == "" {
			return out
		}
		cursor = res.NextCursor
	}
}

// TestTransactionListPagesWithoutRepeats: pages are newest first by (executed_at, sequence) and
// walking them returns every trade exactly once even when executed_at values are equal.
func TestTransactionListPagesWithoutRepeats(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	base := now().Add(-24 * time.Hour)
	want := map[domain.ID]bool{}
	for i := range 9 {
		at := base.Add(time.Duration(i/3) * time.Hour) // three share each executed_at
		want[insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "1", "10", at)).ID] = true
	}

	got := listAll(t, store, user.ID, p.ID, 2, nil, nil, nil, nil)

	if len(got) != len(want) {
		t.Fatalf("listed %d trades, want %d", len(got), len(want))
	}
	for i, tr := range got {
		if !want[tr.ID] {
			t.Fatalf("trade %s listed twice or unknown", tr.ID)
		}
		delete(want, tr.ID)
		if i > 0 {
			prev := got[i-1]
			if tr.ExecutedAt.After(prev.ExecutedAt) || (tr.ExecutedAt.Equal(prev.ExecutedAt) && tr.Sequence >= prev.Sequence) {
				t.Fatalf("item %d out of order (executed_at DESC, sequence DESC)", i)
			}
		}
	}
}

// TestTransactionListFilters: symbol, side and the inclusive from/to bounds combine. Each trade
// outside the result is excluded by exactly one filter, so ignoring any filter changes the result.
func TestTransactionListFilters(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	aapl, msft := insertInstrument(t, store), insertInstrument(t, store)
	from := now().Add(-24 * time.Hour)
	to := from.Add(2 * time.Hour)
	trade := func(i domain.Instrument, side domain.Side, at time.Time) domain.Transaction {
		return insertTrade(t, store, user.ID, newTrade(t, p.ID, i, side, "1", "10", at))
	}
	atFrom := trade(aapl, domain.SideSell, from)
	inside := trade(aapl, domain.SideSell, from.Add(time.Hour))
	atTo := trade(aapl, domain.SideSell, to)
	trade(msft, domain.SideSell, from.Add(time.Hour))         // other symbol
	trade(aapl, domain.SideBuy, from.Add(time.Hour))          // other side
	trade(aapl, domain.SideSell, from.Add(-time.Microsecond)) // before from
	trade(aapl, domain.SideSell, to.Add(time.Microsecond))    // after to

	sell := domain.SideSell
	got := listAll(t, store, user.ID, p.ID, 1, &aapl.Symbol, &sell, &from, &to)

	want := []domain.ID{atTo.ID, inside.ID, atFrom.ID}
	if len(got) != len(want) {
		t.Fatalf("filtered list has %d trades, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("filtered[%d] = %s, want %s", i, got[i].ID, want[i])
		}
	}
}

// TestTransactionListRejectsCursorFromOtherFilters: a cursor only continues the list it came from.
func TestTransactionListRejectsCursorFromOtherFilters(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	for range 3 {
		insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "1", "10", now()))
	}
	unfiltered, _ := domain.NewTransactionFilter(page(t, 1, ""), nil, nil, nil, nil)
	first, err := store.Transactions().List(t.Context(), user.ID, p.ID, unfiltered)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", first, err)
	}

	buy := domain.SideBuy
	other, _ := domain.NewTransactionFilter(page(t, 1, first.NextCursor), nil, &buy, nil, nil)
	_, err = store.Transactions().List(t.Context(), user.ID, p.ID, other)
	requireCode(t, err, domain.CodeInvalidInput)

	portfoliosCursor := encodeCursor(cursorPosition{Kind: cursorKindPortfolios, At: now(), ID: newID(t).String()})
	wrongKind, _ := domain.NewTransactionFilter(page(t, 1, portfoliosCursor), nil, nil, nil, nil)
	_, err = store.Transactions().List(t.Context(), user.ID, p.ID, wrongKind)
	requireCode(t, err, domain.CodeInvalidInput)
}

func TestPortfolioDeleteOwnedEmptyRefusesWhileTradesRemain(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	trade := insertTrade(t, store, user.ID, newTrade(t, p.ID, insertInstrument(t, store), domain.SideBuy, "1", "10", now()))

	e := auditEvent(t, `{}`)
	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		if err := r.Portfolios().DeleteOwnedEmpty(t.Context(), user.ID, p.ID); !errors.Is(err, domain.ErrPortfolioNotEmpty) {
			t.Errorf("DeleteOwnedEmpty with a trade error = %v, want portfolio_not_empty", err)
		}
		return r.Audit().Append(t.Context(), e) // the refusal leaves the transaction usable
	})
	if err != nil || !auditExists(t, e.ID) {
		t.Fatalf("transaction after portfolio_not_empty: %v", err)
	}
	if _, err := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); err != nil {
		t.Fatalf("portfolio was deleted with its trade: %v", err)
	}
	if err := store.Transactions().Delete(t.Context(), user.ID, p.ID, trade.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Portfolios().DeleteOwnedEmpty(t.Context(), user.ID, p.ID); err != nil {
		t.Fatalf("DeleteOwnedEmpty after removing the trade: %v", err)
	}
	if _, err := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted portfolio still readable: %v", err)
	}
}

// TestPortfolioDeleteRacingAnUnlockedInsertIsNotEmpty: a trade inserted by a caller that skipped
// LockOwned makes the DELETE wait on the trade's foreign key lock; once that trade commits, the
// delete fails with the foreign key violation, which DeleteOwnedEmpty reports as not empty.
func TestPortfolioDeleteRacingAnUnlockedInsertIsNotEmpty(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	inserted := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- store.withinTx(t.Context(), func(r *txRepositories) error {
			if _, err := r.Transactions().Insert(t.Context(), user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "1", "10", now())); err != nil {
				return err
			}
			close(inserted)
			<-release
			return nil
		})
	}()
	<-inserted
	deleted := make(chan error, 1)
	go func() { deleted <- store.Portfolios().DeleteOwnedEmpty(t.Context(), user.ID, p.ID) }()
	waitForLockWait(t, "DELETE FROM app.portfolios")
	close(release)

	if err := <-done; err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	if err := <-deleted; !errors.Is(err, domain.ErrPortfolioNotEmpty) {
		t.Fatalf("DeleteOwnedEmpty racing an insert error = %v, want portfolio_not_empty", err)
	}
}

// waitForLockWait polls until a session running a statement that starts with prefix is waiting
// for a lock, so a test can release the blocker at the right moment.
func waitForLockWait(t *testing.T, prefix string) {
	t.Helper()
	admin := openPool(t, "TEST_ADMIN_DATABASE_URL")
	for range 200 {
		var n int
		if err := admin.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%' || $1 || '%'`, prefix).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("statement never waited for a lock")
}

func newTrade(t *testing.T, portfolioID domain.ID, instrument domain.Instrument, side domain.Side, quantity, price string, executedAt time.Time) domain.Transaction {
	t.Helper()
	q, err := domain.ParseQuantity(quantity)
	if err != nil {
		t.Fatal(err)
	}
	p, err := domain.ParsePrice(price)
	if err != nil {
		t.Fatal(err)
	}
	fee, err := domain.ParseFee("1.25")
	if err != nil {
		t.Fatal(err)
	}
	at := now()
	return domain.Transaction{
		ID: newID(t), PortfolioID: portfolioID, InstrumentID: instrument.ID, Side: side, Quantity: q, Price: p, Fee: fee,
		ExecutedAt: executedAt, CreatedAt: at, UpdatedAt: at,
	}
}

func insertTrade(t *testing.T, store *Store, ownerID domain.ID, trade domain.Transaction) domain.Transaction {
	t.Helper()
	got, err := store.Transactions().Insert(t.Context(), ownerID, trade)
	if err != nil {
		t.Fatalf("insert trade: %v", err)
	}
	return got
}

func requireCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

// insertInstrument stores a fresh instrument with a random symbol and provider asset id.
func insertInstrument(t *testing.T, store *Store) domain.Instrument {
	t.Helper()
	got, err := store.Instruments().Upsert(t.Context(), newInstrument(t, randomSymbol(t), "asset-"+newID(t).String()))
	if err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	return got
}

// TestTransactionListRejectsCursorFromAnotherPortfolio: a cursor continues the list of the
// portfolio it came from; on another portfolio it would silently skip rows.
func TestTransactionListRejectsCursorFromAnotherPortfolio(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	a, b := insertPortfolio(t, store, user.ID), insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	for range 3 {
		insertTrade(t, store, user.ID, newTrade(t, a.ID, instrument, domain.SideBuy, "1", "10", now()))
		insertTrade(t, store, user.ID, newTrade(t, b.ID, instrument, domain.SideBuy, "1", "10", now()))
	}
	firstFilter, _ := domain.NewTransactionFilter(page(t, 1, ""), nil, nil, nil, nil)
	first, err := store.Transactions().List(t.Context(), user.ID, a.ID, firstFilter)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page of A = %+v, %v", first, err)
	}

	onB, _ := domain.NewTransactionFilter(page(t, 1, first.NextCursor), nil, nil, nil, nil)
	_, err = store.Transactions().List(t.Context(), user.ID, b.ID, onB)

	requireCode(t, err, domain.CodeInvalidInput)
}
