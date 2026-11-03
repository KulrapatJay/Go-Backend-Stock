package http

import (
	"bytes"
	"context"
	"errors"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The rules of milestone 7 proved through the HTTP layer against the real database: replay of the
// whole history, idempotency keys, isolation between portfolios and users, and atomic audit.

// trade is a valid body for side and quantity of symbol at a price of 100 with no fee.
func (h *authHarness) trade(symbol, side, quantity string, at time.Time) map[string]string {
	req := h.tradeRequest(symbol)
	req["side"], req["quantity"], req["price"], req["fee"] = side, quantity, "100", "0"
	req["executed_at"] = at.UTC().Format(time.RFC3339)
	return req
}

// tradeDays returns the start of day i of a four-day history that ends a day before the harness
// clock, so every trade of a test is in the past.
func (h *authHarness) tradeDays() func(day int) time.Time {
	base := h.clock.Now().Add(-5 * 24 * time.Hour).Truncate(time.Second)
	return func(day int) time.Time { return base.Add(time.Duration(day) * 24 * time.Hour) }
}

// idempotencyRecord reads the key a portfolio stored: when it was made and which transaction it
// replays. found is false when no row holds the key.
type idempotencyRecord struct {
	Found     bool
	CreatedAt time.Time
	ExpiresAt time.Time
	ResultID  string
}

func (h *authHarness) idempotencyRecord(t *testing.T, portfolioID, key string) idempotencyRecord {
	t.Helper()
	var r idempotencyRecord
	err := h.admin.QueryRow(context.Background(),
		`SELECT created_at, expires_at, result->>'id' FROM app.idempotency_keys WHERE portfolio_id = $1 AND key = $2`,
		portfolioID, key).Scan(&r.CreatedAt, &r.ExpiresAt, &r.ResultID)
	if errors.Is(err, pgx.ErrNoRows) {
		return r
	}
	if err != nil {
		t.Fatalf("read idempotency key: %v", err)
	}
	r.Found = true
	return r
}

// auditCount counts the events of one type written about trades or the portfolio itself, by the
// portfolio_id their metadata or target carries.
func (h *authHarness) auditCount(t *testing.T, eventType, portfolioID string) int {
	t.Helper()
	var n int
	if err := h.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM app.audit_events
		  WHERE event_type = $1 AND (metadata->>'portfolio_id' = $2 OR target_id = $2)`,
		eventType, portfolioID).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

// historyNeverNegative replays the stored rows of the portfolio in SQL, independently of the Go
// replay: per instrument in executed_at, sequence order, the running quantity never drops below 0.
func (h *authHarness) historyNeverNegative(t *testing.T, portfolioID string) bool {
	t.Helper()
	var ok bool
	if err := h.admin.QueryRow(context.Background(),
		`SELECT COALESCE(bool_and(running >= 0), true) FROM (
		   SELECT sum(CASE WHEN side = 'buy' THEN quantity ELSE -quantity END)
		          OVER (PARTITION BY instrument_id ORDER BY executed_at, sequence) AS running
		     FROM app.transactions WHERE portfolio_id = $1) r`, portfolioID).Scan(&ok); err != nil {
		t.Fatalf("replay history: %v", err)
	}
	return ok
}

// heldQuantity is the shares of symbol the stored rows of the portfolio add up to, as a decimal
// string without trailing zeros ("0" when there are none).
func (h *authHarness) heldQuantity(t *testing.T, portfolioID, symbol string) string {
	t.Helper()
	var q string
	if err := h.admin.QueryRow(context.Background(),
		`SELECT trim_scale(COALESCE(sum(CASE WHEN t.side = 'buy' THEN t.quantity ELSE -t.quantity END), 0))::text
		   FROM app.transactions t JOIN app.instruments i ON i.id = t.instrument_id
		  WHERE t.portfolio_id = $1 AND i.symbol = $2`, portfolioID, symbol).Scan(&q); err != nil {
		t.Fatalf("held quantity: %v", err)
	}
	return q
}

// tradeIDs lists the trades of a portfolio newest first, as the API orders them.
func (b *browser) tradeIDs(portfolioID string) []string {
	b.t.Helper()
	resp, body := b.listTrades(portfolioID, "limit=100")
	if resp.StatusCode != nethttp.StatusOK {
		b.t.Fatalf("list = %d %s", resp.StatusCode, body)
	}
	var ids []string
	for _, tr := range decodeTransactionPage(b.t, body).Data {
		ids = append(ids, tr.ID)
	}
	return ids
}

func TestBackdatedSellThatBreaksALaterSellIsRefused(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	day := h.tradeDays()
	b.mustCreateTrade(p.ID, h.trade(symbol, "buy", "10", day(1)))
	laterSell := b.mustCreateTrade(p.ID, h.trade(symbol, "sell", "10", day(3)))
	keysBefore := h.countRows(t, "idempotency_keys", p.ID)

	key := newKey(t)
	backdated := h.trade(symbol, "sell", "1", day(2))
	resp, body := b.createTrade(p.ID, key, backdated)
	if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "insufficient_shares" {
		t.Fatalf("backdated sell = %d %s, want 422 insufficient_shares", resp.StatusCode, body)
	}
	if n := h.countRows(t, "transactions", p.ID); n != 2 {
		t.Errorf("transactions = %d, want 2 (the refused sell rolled back)", n)
	}
	if rec := h.idempotencyRecord(t, p.ID, key); rec.Found {
		t.Errorf("the key of the refused sell was stored: %+v", rec)
	}
	if n := h.countRows(t, "idempotency_keys", p.ID); n != keysBefore {
		t.Errorf("idempotency keys = %d, want %d", n, keysBefore)
	}
	if n := h.auditCount(t, "transaction.created", p.ID); n != 2 {
		t.Errorf("transaction.created events = %d, want 2 (none for the refused sell)", n)
	}

	if resp, body := b.deleteTrade(p.ID, laterSell.ID, `"1"`); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("delete the later sell = %d %s", resp.StatusCode, body)
	}
	resp, body = b.createTrade(p.ID, key, backdated)
	if resp.StatusCode != nethttp.StatusCreated {
		t.Fatalf("the same key after the later sell is gone = %d %s, want 201", resp.StatusCode, body)
	}
	created := decodeTransaction(t, body)
	if rec := h.idempotencyRecord(t, p.ID, key); !rec.Found || rec.ResultID != created.ID {
		t.Errorf("key record = %+v, want it to replay %s", rec, created.ID)
	}
	if n := h.countRows(t, "transactions", p.ID); n != 2 {
		t.Errorf("transactions = %d, want the buy and the backdated sell", n)
	}
	if q := h.heldQuantity(t, p.ID, symbol); q != "9" {
		t.Errorf("held = %s, want 9", q)
	}
}

func TestEditOrDeleteThatBreaksHistoryIsRefused(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	day := h.tradeDays()
	buy := b.mustCreateTrade(p.ID, h.trade(symbol, "buy", "10", day(1)))
	sell := b.mustCreateTrade(p.ID, h.trade(symbol, "sell", "10", day(2)))
	h.clock.Advance(time.Minute) // a write that slipped through would show a new updated_at

	snapshot := func() ([]byte, []byte, tradeState, tradeState) {
		t.Helper()
		_, buyBody := b.getTrade(p.ID, buy.ID)
		_, sellBody := b.getTrade(p.ID, sell.ID)
		return buyBody, sellBody, h.tradeState(t, buy.ID), h.tradeState(t, sell.ID)
	}
	buyBefore, sellBefore, buyState, sellState := snapshot()
	eventsBefore := len(h.auditByTarget(t, buy.ID)) + len(h.auditByTarget(t, sell.ID))

	smaller := h.trade(symbol, "buy", "5", day(1))
	moved := h.trade(symbol, "buy", "10", day(3))
	for name, attempt := range map[string]func() (response, []byte){
		"buy reduced below the sale":  func() (response, []byte) { return b.updateTrade(p.ID, buy.ID, `"1"`, smaller) },
		"buy moved after the sale":    func() (response, []byte) { return b.updateTrade(p.ID, buy.ID, `"1"`, moved) },
		"buy deleted before the sale": func() (response, []byte) { return b.deleteTrade(p.ID, buy.ID, `"1"`) },
	} {
		resp, body := attempt()
		if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "invalid_history" {
			t.Errorf("%s = %d %s, want 422 invalid_history", name, resp.StatusCode, body)
		}
		buyAfter, sellAfter, buyNow, sellNow := snapshot()
		if !bytes.Equal(buyAfter, buyBefore) || !bytes.Equal(sellAfter, sellBefore) || buyNow != buyState || sellNow != sellState {
			t.Errorf("%s changed the trades:\nbuy  %s\nwant %s\nsell %s\nwant %s", name, buyAfter, buyBefore, sellAfter, sellBefore)
		}
		if n := len(h.auditByTarget(t, buy.ID)) + len(h.auditByTarget(t, sell.ID)); n != eventsBefore {
			t.Errorf("%s: audit events = %d, want %d", name, n, eventsBefore)
		}
	}
	if buyState.Version != 1 || sellState.Version != 1 {
		t.Errorf("versions = %d and %d, want 1", buyState.Version, sellState.Version)
	}
	if !h.historyNeverNegative(t, p.ID) {
		t.Error("the stored history goes below zero")
	}
}

// Trades executed at the same instant replay by sequence, the order the database stored them,
// never by UUID or by the order a later edit arrived in.
func TestTradesAtTheSameTimeReplayInSequenceOrder(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	day := h.tradeDays()

	{ // An edit keeps its sequence.
		p := b.mustCreatePortfolio("Edited", "")
		symbol := h.freshSymbol(t)
		a := b.mustCreateTrade(p.ID, h.trade(symbol, "buy", "5", day(1)))
		s := b.mustCreateTrade(p.ID, h.trade(symbol, "sell", "5", day(2)))
		c := b.mustCreateTrade(p.ID, h.trade(symbol, "buy", "5", day(3)))
		if seqOf(t, a.Sequence) >= seqOf(t, s.Sequence) || seqOf(t, s.Sequence) >= seqOf(t, c.Sequence) {
			t.Fatalf("sequences %s, %s, %s are not increasing", a.Sequence, s.Sequence, c.Sequence)
		}

		resp, body := b.updateTrade(p.ID, c.ID, `"1"`, h.trade(symbol, "buy", "5", day(2)))
		if resp.StatusCode != nethttp.StatusOK {
			t.Fatalf("move C to the time of S = %d %s, want 200", resp.StatusCode, body)
		}
		if got := decodeTransaction(t, body); got.Sequence != c.Sequence || !got.ExecutedAt.Equal(day(2)) {
			t.Errorf("edited C = %+v, want sequence %s at %s", got, c.Sequence, day(2))
		}
		if state := h.tradeState(t, c.ID); state.Sequence != c.Sequence || state.Version != 2 {
			t.Errorf("stored C = %+v, want sequence %s at version 2", state, c.Sequence)
		}
		// Newest first: at the same time the higher sequence comes first.
		if ids := b.tradeIDs(p.ID); !slices.Equal(ids, []string{c.ID, s.ID, a.ID}) {
			t.Errorf("list order = %v, want C, S, A", ids)
		}

		// Without A, S (the lower sequence) sells before C buys: the history breaks.
		aState := h.tradeState(t, a.ID)
		resp, body = b.deleteTrade(p.ID, a.ID, `"1"`)
		if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "invalid_history" {
			t.Errorf("delete A = %d %s, want 422 invalid_history", resp.StatusCode, body)
		}
		if after := h.tradeState(t, a.ID); after != aState {
			t.Errorf("A after the refused delete = %+v, want %+v", after, aState)
		}
		if n := h.countRows(t, "transactions", p.ID); n != 3 {
			t.Errorf("transactions = %d, want 3", n)
		}
	}

	// The trades were stored with UUIDs in the opposite order of their sequences, so a replay that
	// broke the tie by id would put C before S and accept the delete.
	{
		p := b.mustCreatePortfolio("Seeded", "")
		symbol := h.freshSymbol(t)
		a := b.mustCreateTrade(p.ID, h.trade(symbol, "buy", "5", day(1)))
		instrument := h.instrumentID(t, symbol)
		low, high := mustTestID(t).String(), mustTestID(t).String()
		if low >= high {
			t.Fatalf("UUIDv7 %s is not before %s", low, high)
		}
		sID, cID := high, low // S is stored first, so it gets the lower sequence and the higher id
		for _, row := range []struct{ id, side string }{{sID, "sell"}, {cID, "buy"}} {
			if _, err := h.admin.Exec(context.Background(),
				`INSERT INTO app.transactions (id, portfolio_id, instrument_id, side, quantity, price, fee, executed_at, created_at, updated_at)
				 VALUES ($1, $2, $3, $4, 5, 100, 0, $5, $6, $6)`,
				row.id, p.ID, instrument, row.side, day(2), h.clock.Now()); err != nil {
				t.Fatalf("seed %s: %v", row.side, err)
			}
		}
		if seqS, seqC := seqOf(t, h.tradeState(t, sID).Sequence), seqOf(t, h.tradeState(t, cID).Sequence); seqS >= seqC {
			t.Fatalf("sequence of S %d is not below C %d", seqS, seqC)
		}
		if ids := b.tradeIDs(p.ID); !slices.Equal(ids, []string{cID, sID, a.ID}) {
			t.Errorf("list order = %v, want C, S, A by sequence", ids)
		}

		resp, body := b.deleteTrade(p.ID, a.ID, `"1"`)
		if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "invalid_history" {
			t.Errorf("delete A = %d %s, want 422 invalid_history", resp.StatusCode, body)
		}
		if n := h.countRows(t, "transactions", p.ID); n != 3 {
			t.Errorf("transactions = %d, want 3", n)
		}
	}
}

// seqOf is a sequence as a number, for ordering.
func seqOf(t *testing.T, sequence string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(sequence, 10, 64)
	if err != nil {
		t.Fatalf("sequence %q: %v", sequence, err)
	}
	return n
}

func TestSameSymbolInTwoPortfoliosStaysSeparate(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	long := b.mustCreatePortfolio("Long term", "")
	short := b.mustCreatePortfolio("Short term", "")
	symbol := h.freshSymbol(t)
	day := h.tradeDays()
	b.mustCreateTrade(long.ID, h.trade(symbol, "buy", "10", day(1)))

	resp, body := b.createTrade(short.ID, newKey(t), h.trade(symbol, "sell", "1", day(2)))
	if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "insufficient_shares" {
		t.Errorf("sell in the other portfolio = %d %s, want 422 insufficient_shares", resp.StatusCode, body)
	}
	if n := h.countRows(t, "transactions", short.ID); n != 0 {
		t.Errorf("transactions in the other portfolio = %d, want 0", n)
	}

	// Each portfolio counts its own shares: 3 bought and sold in one leave the 10 of the other.
	b.mustCreateTrade(short.ID, h.trade(symbol, "buy", "3", day(2)))
	b.mustCreateTrade(short.ID, h.trade(symbol, "sell", "3", day(3)))
	if q := h.heldQuantity(t, long.ID, symbol); q != "10" {
		t.Errorf("held in the first portfolio = %s, want 10", q)
	}
	b.mustCreateTrade(long.ID, h.trade(symbol, "sell", "10", day(3)))
	resp, body = b.createTrade(long.ID, newKey(t), h.trade(symbol, "sell", "0.0000000001", day(4)))
	if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "insufficient_shares" {
		t.Errorf("sell past the first portfolio's shares = %d %s, want 422 insufficient_shares", resp.StatusCode, body)
	}
	if q1, q2 := h.heldQuantity(t, long.ID, symbol), h.heldQuantity(t, short.ID, symbol); q1 != "0" || q2 != "0" {
		t.Errorf("held = %s and %s, want 0 and 0", q1, q2)
	}
}

func TestSymbolChangeThatBreaksTheOldSymbolRollsBackEverything(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	oldSymbol, newSymbol := h.freshSymbol(t), h.freshSymbol(t)
	day := h.tradeDays()
	buy := b.mustCreateTrade(p.ID, h.trade(oldSymbol, "buy", "10", day(1)))
	sell := b.mustCreateTrade(p.ID, h.trade(oldSymbol, "sell", "5", day(2)))
	buyState, sellState := h.tradeState(t, buy.ID), h.tradeState(t, sell.ID)
	eventsBefore := len(h.auditByTarget(t, buy.ID))

	resp, body := b.updateTrade(p.ID, buy.ID, `"1"`, h.trade(newSymbol, "buy", "10", day(1)))
	if resp.StatusCode != nethttp.StatusUnprocessableEntity || errorCode(t, body) != "invalid_history" {
		t.Fatalf("move the buy to %s = %d %s, want 422 invalid_history", newSymbol, resp.StatusCode, body)
	}
	if n := h.instruments.lookups(newSymbol); n != 1 {
		t.Errorf("provider lookups of %s = %d, want 1 (the symbol was verified before the replay)", newSymbol, n)
	}
	if h.instrumentExists(t, newSymbol) {
		t.Errorf("instrument %s was stored by a change that rolled back", newSymbol)
	}
	if after := h.tradeState(t, buy.ID); after != buyState || after.Symbol != oldSymbol {
		t.Errorf("buy = %+v, want %+v", after, buyState)
	}
	if after := h.tradeState(t, sell.ID); after != sellState {
		t.Errorf("sell = %+v, want %+v", after, sellState)
	}
	if n := len(h.auditByTarget(t, buy.ID)); n != eventsBefore {
		t.Errorf("audit events = %d, want %d", n, eventsBefore)
	}
}

func TestRetryAfterEditOrDeleteReturnsTheFirstResponse(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	req := h.tradeRequest(symbol)
	key := newKey(t)

	first, firstBody := b.createTrade(p.ID, key, req)
	if first.StatusCode != nethttp.StatusCreated {
		t.Fatalf("create = %d %s", first.StatusCode, firstBody)
	}
	created := decodeTransaction(t, firstBody)
	h.clock.Advance(time.Minute)

	if resp, body := b.updateTrade(p.ID, created.ID, `"1"`, h.replacement(symbol, "250")); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, body)
	}
	edited := h.tradeState(t, created.ID)
	retry := func(stage string, wantRows, wantEvents int) {
		t.Helper()
		resp, body := b.createTrade(p.ID, key, req)
		if resp.StatusCode != nethttp.StatusCreated || !bytes.Equal(body, firstBody) {
			t.Errorf("retry after %s = %d\n%s\nwant 201 with the first body\n%s", stage, resp.StatusCode, body, firstBody)
		}
		if v := resp.Header.Get("ETag"); v != `"1"` {
			t.Errorf("retry after %s: ETag = %q, want the first \"1\"", stage, v)
		}
		if n := h.countRows(t, "transactions", p.ID); n != wantRows {
			t.Errorf("retry after %s: transactions = %d, want %d", stage, n, wantRows)
		}
		if n := len(h.auditByTarget(t, created.ID)); n != wantEvents {
			t.Errorf("retry after %s: audit events = %d, want %d", stage, n, wantEvents)
		}
	}

	retry("the edit", 1, 2)
	if after := h.tradeState(t, created.ID); after != edited || after.Version != 2 || after.Price != "250.0000000000" {
		t.Errorf("trade after the retry = %+v, want the edit kept at version 2", after)
	}

	if resp, body := b.deleteTrade(p.ID, created.ID, `"2"`); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("delete = %d %s", resp.StatusCode, body)
	}
	retry("the delete", 0, 3)
	if state := h.tradeState(t, created.ID); state.Found {
		t.Errorf("the retry brought the deleted trade back: %+v", state)
	}
}

func TestSameKeyInAnotherPortfolioOrUserIsANewRequest(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	other, _ := signedIn(t, h)
	first := b.mustCreatePortfolio("Long term", "")
	second := b.mustCreatePortfolio("Short term", "")
	foreign := other.mustCreatePortfolio("Long term", "")
	req := h.tradeRequest(h.freshSymbol(t))
	key := newKey(t)

	seen := map[string]bool{}
	for _, tc := range []struct {
		name        string
		who         *browser
		portfolioID string
	}{
		{"first portfolio", b, first.ID},
		{"another portfolio of the same user", b, second.ID},
		{"a portfolio of another user", other, foreign.ID},
	} {
		resp, body := tc.who.createTrade(tc.portfolioID, key, req)
		if resp.StatusCode != nethttp.StatusCreated {
			t.Fatalf("%s = %d %s, want 201", tc.name, resp.StatusCode, body)
		}
		got := decodeTransaction(t, body)
		if seen[got.ID] || got.PortfolioID != tc.portfolioID {
			t.Errorf("%s replayed another scope's trade: %+v", tc.name, got)
		}
		seen[got.ID] = true
		if n := h.countRows(t, "transactions", tc.portfolioID); n != 1 {
			t.Errorf("%s: transactions = %d, want 1", tc.name, n)
		}
		if rec := h.idempotencyRecord(t, tc.portfolioID, key); !rec.Found || rec.ResultID != got.ID {
			t.Errorf("%s: key record = %+v, want it to hold %s", tc.name, rec, got.ID)
		}
		if n := h.auditCount(t, "transaction.created", tc.portfolioID); n != 1 {
			t.Errorf("%s: transaction.created events = %d, want 1", tc.name, n)
		}
	}
}

func TestKeyWithAnotherPayloadConflicts(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	at := h.clock.Now().Add(-time.Hour).Truncate(time.Second)
	req := map[string]string{"symbol": symbol, "side": "buy", "quantity": "1", "price": "200", "fee": "1", "executed_at": at.Format(time.RFC3339)}
	key := newKey(t)
	resp, firstBody := b.createTrade(p.ID, key, req)
	if resp.StatusCode != nethttp.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, firstBody)
	}
	created := decodeTransaction(t, firstBody)
	other := h.freshSymbol(t)
	b.mustCreateTrade(p.ID, h.tradeRequest(other)) // a stored symbol, so the conflict is the only refusal

	with := func(field, value string) map[string]string {
		m := map[string]string{}
		for k, v := range req {
			m[k] = v
		}
		m[field] = value
		return m
	}
	for name, body := range map[string]map[string]string{
		"symbol":      with("symbol", other),
		"side":        with("side", "sell"),
		"quantity":    with("quantity", "1.0000000001"),
		"price":       with("price", "200.01"),
		"fee":         with("fee", "0"),
		"executed_at": with("executed_at", at.Add(time.Second).Format(time.RFC3339)),
	} {
		resp, data := b.createTrade(p.ID, key, body)
		if resp.StatusCode != nethttp.StatusConflict || errorCode(t, data) != "idempotency_conflict" {
			t.Errorf("same key, other %s = %d %s, want 409 idempotency_conflict", name, resp.StatusCode, data)
		}
	}

	// The same trade written another way is the same request.
	for name, body := range map[string]map[string]string{
		`quantity "1.0"`:             with("quantity", "1.0"),
		`price "200.000"`:            with("price", "200.000"),
		`fee "1.0000000000"`:         with("fee", "1.0000000000"),
		"lower-case symbol":          with("symbol", strings.ToLower(symbol)),
		"the same instant at +07:00": with("executed_at", at.In(time.FixedZone("ICT", 7*3600)).Format(time.RFC3339)),
	} {
		resp, data := b.createTrade(p.ID, key, body)
		if resp.StatusCode != nethttp.StatusCreated || !bytes.Equal(data, firstBody) {
			t.Errorf("same key, %s = %d %s, want the first 201 body", name, resp.StatusCode, data)
		}
	}
	if n := h.countRows(t, "transactions", p.ID); n != 2 {
		t.Errorf("transactions = %d, want 2", n)
	}
	if n := len(h.auditByTarget(t, created.ID)); n != 1 {
		t.Errorf("audit events of the keyed trade = %d, want 1", n)
	}
	if n := h.auditCount(t, "transaction.created", p.ID); n != 2 {
		t.Errorf("transaction.created events = %d, want 2", n)
	}
}

func TestExpiredKeyCreatesANewTrade(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	req := h.tradeRequest(h.freshSymbol(t))
	key := newKey(t)
	resp, firstBody := b.createTrade(p.ID, key, req)
	if resp.StatusCode != nethttp.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, firstBody)
	}
	first := decodeTransaction(t, firstBody)
	firstRecord := h.idempotencyRecord(t, p.ID, key)
	if !firstRecord.Found || !firstRecord.ExpiresAt.Equal(firstRecord.CreatedAt.Add(24*time.Hour)) {
		t.Fatalf("key record = %+v, want it to expire 24 hours after it was made", firstRecord)
	}

	h.clock.Advance(23 * time.Hour)
	var body []byte
	resp, body = b.createTrade(p.ID, key, req)
	if resp.StatusCode != nethttp.StatusCreated || !bytes.Equal(body, firstBody) {
		t.Fatalf("retry at 23 hours = %d %s, want the first body", resp.StatusCode, body)
	}
	if rec := h.idempotencyRecord(t, p.ID, key); rec.ResultID != first.ID || !rec.CreatedAt.Equal(firstRecord.CreatedAt) || !rec.ExpiresAt.Equal(firstRecord.ExpiresAt) {
		t.Errorf("key record after a retry = %+v, want %+v (a retry does not extend it)", rec, firstRecord)
	}

	h.clock.Advance(time.Hour + time.Second)
	resp, body = b.createTrade(p.ID, key, req)
	if resp.StatusCode != nethttp.StatusCreated {
		t.Fatalf("retry after 24 hours = %d %s, want 201", resp.StatusCode, body)
	}
	second := decodeTransaction(t, body)
	if second.ID == first.ID || bytes.Equal(body, firstBody) || !second.CreatedAt.Equal(h.clock.Now()) {
		t.Errorf("retry after 24 hours = %+v, want a new trade created now", second)
	}
	if n := h.countRows(t, "transactions", p.ID); n != 2 {
		t.Errorf("transactions = %d, want 2", n)
	}
	rec := h.idempotencyRecord(t, p.ID, key)
	if !rec.Found || rec.ResultID != second.ID || !rec.CreatedAt.Equal(h.clock.Now()) || !rec.ExpiresAt.Equal(h.clock.Now().Add(24*time.Hour)) {
		t.Errorf("key record = %+v, want a new one for %s made now", rec, second.ID)
	}
	if n := h.countRows(t, "idempotency_keys", p.ID); n != 1 {
		t.Errorf("idempotency keys = %d, want 1 (the expired record was replaced)", n)
	}
	if resp, again := b.createTrade(p.ID, key, req); resp.StatusCode != nethttp.StatusCreated || !bytes.Equal(again, body) {
		t.Errorf("retry of the new key = %d %s, want the second body", resp.StatusCode, again)
	}
	if n := h.auditCount(t, "transaction.created", p.ID); n != 2 {
		t.Errorf("transaction.created events = %d, want 2", n)
	}
}

func TestTradesAreIsolatedBetweenUsers(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	owner, _ := signedIn(t, h)
	p := owner.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	created := owner.mustCreateTrade(p.ID, h.tradeRequest(symbol))
	_, ownerView := owner.getTrade(p.ID, created.ID)
	state := h.tradeState(t, created.ID)

	stranger, strangerUser := signedIn(t, h)
	unknownPortfolio, unknownTrade := mustTestID(t).String(), mustTestID(t).String()
	attempts := []struct {
		name  string
		write bool
		do    func(portfolioID, tradeID string) (response, []byte)
	}{
		{"GET", false, func(pid, id string) (response, []byte) { return stranger.getTrade(pid, id) }},
		{"list", false, func(pid, _ string) (response, []byte) { return stranger.listTrades(pid, "") }},
		{"PUT", true, func(pid, id string) (response, []byte) {
			return stranger.updateTrade(pid, id, `"1"`, h.replacement(symbol, "1"))
		}},
		{"DELETE", true, func(pid, id string) (response, []byte) { return stranger.deleteTrade(pid, id, `"1"`) }},
		{"POST", true, func(pid, _ string) (response, []byte) {
			return stranger.createTrade(pid, newKey(t), h.tradeRequest(symbol))
		}},
	}
	writes := 0
	for _, a := range attempts {
		foreignResp, foreignBody := a.do(p.ID, created.ID)
		unknownResp, unknownBody := a.do(unknownPortfolio, unknownTrade)
		if foreignResp.StatusCode != nethttp.StatusNotFound || unknownResp.StatusCode != nethttp.StatusNotFound {
			t.Errorf("%s: another user's trade = %d, an unknown one = %d, want 404 for both", a.name, foreignResp.StatusCode, unknownResp.StatusCode)
			continue
		}
		if x, y := stripJSONField(t, foreignBody, "error", "request_id"), stripJSONField(t, unknownBody, "error", "request_id"); x != y {
			t.Errorf("%s: the two 404 bodies differ:\n%s\n%s", a.name, x, y)
		}
		if a.write {
			writes++
		}
		if n := len(accessDenied(h.auditByTarget(t, p.ID))); n != writes {
			t.Errorf("after %s: access_denied events = %d, want %d (writes only)", a.name, n, writes)
		}
	}

	denied := accessDenied(h.auditByTarget(t, p.ID))
	for _, e := range denied {
		if e.Actor != strangerUser.ID || e.Reason != "not_owner" || e.Outcome != "denied" {
			t.Errorf("access_denied = %+v, want actor %s, reason not_owner, outcome denied", e, strangerUser.ID)
		}
	}
	if got := h.auditByTarget(t, unknownPortfolio); len(got) != 0 {
		t.Errorf("events for a portfolio nobody owns = %+v, want none", got)
	}
	if after := h.tradeState(t, created.ID); after != state {
		t.Errorf("trade after the stranger's calls = %+v, want %+v", after, state)
	}
	if _, view := owner.getTrade(p.ID, created.ID); !bytes.Equal(view, ownerView) {
		t.Errorf("owner's view changed:\n%s\nwant\n%s", view, ownerView)
	}
	if n := h.countRows(t, "transactions", p.ID); n != 1 {
		t.Errorf("transactions = %d, want only the owner's", n)
	}
	if n := h.countRows(t, "idempotency_keys", p.ID); n != 1 {
		t.Errorf("idempotency keys = %d, want only the owner's", n)
	}
	if n := len(h.auditByTarget(t, created.ID)); n != 1 {
		t.Errorf("events of the trade = %d, want only its creation", n)
	}
}

func TestTradeRollsBackWhenTheAuditFails(t *testing.T) {
	healthy := newAuthHarness(t, withTransactions())
	failing := newAuthHarness(t, withTransactions(), withFailingAudit())
	b, _ := signedIn(t, healthy)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := healthy.freshSymbol(t)
	created := b.mustCreateTrade(p.ID, healthy.tradeRequest(symbol))
	state := healthy.tradeState(t, created.ID)
	keysBefore := healthy.countRows(t, "idempotency_keys", p.ID)
	eventsBefore := len(healthy.auditByTarget(t, created.ID))

	// The same session, served by an API whose audit store is down.
	c := sameUserBrowser(t, failing, b)
	fresh := healthy.freshSymbol(t)
	key := newKey(t)
	for name, attempt := range map[string]func() (response, []byte){
		"POST":                   func() (response, []byte) { return c.createTrade(p.ID, key, healthy.tradeRequest(symbol)) },
		"POST with a new symbol": func() (response, []byte) { return c.createTrade(p.ID, newKey(t), healthy.tradeRequest(fresh)) },
		"PUT": func() (response, []byte) {
			return c.updateTrade(p.ID, created.ID, `"1"`, healthy.replacement(symbol, "300"))
		},
		"DELETE": func() (response, []byte) { return c.deleteTrade(p.ID, created.ID, `"1"`) },
	} {
		resp, body := attempt()
		if resp.StatusCode != nethttp.StatusInternalServerError || errorCode(t, body) != "audit_write_failed" {
			t.Errorf("%s = %d %s, want 500 audit_write_failed", name, resp.StatusCode, body)
		}
	}
	if after := healthy.tradeState(t, created.ID); after != state {
		t.Errorf("trade after the failed changes = %+v, want %+v", after, state)
	}
	if n := healthy.countRows(t, "transactions", p.ID); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
	if n := healthy.countRows(t, "idempotency_keys", p.ID); n != keysBefore {
		t.Errorf("idempotency keys = %d, want %d", n, keysBefore)
	}
	if rec := healthy.idempotencyRecord(t, p.ID, key); rec.Found {
		t.Errorf("the key of the failed POST was stored: %+v", rec)
	}
	if failing.instruments.lookups(fresh) != 1 || healthy.instrumentExists(t, fresh) {
		t.Errorf("new symbol %s: looked up %d times, stored %v; want verified once and rolled back", fresh, failing.instruments.lookups(fresh), healthy.instrumentExists(t, fresh))
	}
	if n := len(healthy.auditByTarget(t, created.ID)); n != eventsBefore {
		t.Errorf("audit events = %d, want %d", n, eventsBefore)
	}

	// Nothing was kept, so the key is still free on a healthy API.
	if resp, body := b.createTrade(p.ID, key, healthy.tradeRequest(symbol)); resp.StatusCode != nethttp.StatusCreated {
		t.Errorf("the key on the healthy API = %d %s, want 201", resp.StatusCode, body)
	}
}

func TestPortfolioWithTradesCannotBeDeletedUntilTheyAreGone(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	b, _ := signedIn(t, h)
	p := b.mustCreatePortfolio("Long term", "")
	symbol := h.freshSymbol(t)
	day := h.tradeDays()
	buy := b.mustCreateTrade(p.ID, h.trade(symbol, "buy", "10", day(1)))
	sell := b.mustCreateTrade(p.ID, h.trade(symbol, "sell", "4", day(2)))

	refused := func(stage string) {
		t.Helper()
		resp, body := b.deletePortfolio(p.ID, `"1"`)
		if resp.StatusCode != nethttp.StatusConflict || errorCode(t, body) != "portfolio_not_empty" {
			t.Errorf("delete the portfolio %s = %d %s, want 409 portfolio_not_empty", stage, resp.StatusCode, body)
		}
		if state := h.portfolioState(t, p.ID); !state.Found || state.Version != 1 {
			t.Errorf("portfolio %s = %+v, want it kept at version 1", stage, state)
		}
		if n := h.countRows(t, "idempotency_keys", p.ID); n != 2 {
			t.Errorf("idempotency keys %s = %d, want 2 (the refused delete keeps them)", stage, n)
		}
	}
	refused("with two trades")
	if resp, body := b.deleteTrade(p.ID, sell.ID, `"1"`); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("delete the sell = %d %s", resp.StatusCode, body)
	}
	refused("with one trade")
	if resp, body := b.deleteTrade(p.ID, buy.ID, `"1"`); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("delete the buy = %d %s", resp.StatusCode, body)
	}

	if resp, body := b.deletePortfolio(p.ID, `"1"`); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("delete the empty portfolio = %d %s, want 204", resp.StatusCode, body)
	}
	if state := h.portfolioState(t, p.ID); state.Found {
		t.Errorf("the portfolio is still stored: %+v", state)
	}
	if n := h.countRows(t, "idempotency_keys", p.ID); n != 0 {
		t.Errorf("idempotency keys = %d, want 0", n)
	}
	if n := h.auditCount(t, "portfolio.deleted", p.ID); n != 1 {
		t.Errorf("portfolio.deleted events = %d, want 1", n)
	}
	if n := h.auditCount(t, "transaction.deleted", p.ID); n != 2 {
		t.Errorf("transaction.deleted events = %d, want 2", n)
	}
}
