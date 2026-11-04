package http

import (
	"bytes"
	nethttp "net/http"
	"slices"
	"sync"
	"testing"
)

// The races of milestone 7 against the real database. Every request of a race has its own browser
// (sharing the session where the same user is meant), and all of them are released together once
// every goroutine exists; no test orders events with sleeps.

// raceAll runs the n requests at the same moment and returns each status and body by index.
func raceAll(t *testing.T, n int, request func(i int) (response, []byte, error)) ([]response, [][]byte) {
	t.Helper()
	var (
		wg     sync.WaitGroup
		start  = make(chan struct{})
		resps  = make([]response, n)
		bodies = make([][]byte, n)
		errs   = make([]error, n)
	)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resps[i], bodies[i], errs[i] = request(i)
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	return resps, bodies
}

// tabs returns n browsers sharing the session of b, each with its own CSRF token.
func tabs(t *testing.T, h *authHarness, b *browser, n int) []*browser {
	t.Helper()
	out := make([]*browser, n)
	for i := range out {
		out[i] = sameUserBrowser(t, h, b)
	}
	return out
}

// isServerBusy is the 503 a request gets when the database lock or statement timeout ends it.
func isServerBusy(t *testing.T, resp response, body []byte) bool {
	t.Helper()
	return resp.StatusCode == nethttp.StatusServiceUnavailable && errorCode(t, body) == "server_busy" && resp.Header.Get("Retry-After") != ""
}

func TestConcurrentSellsNeverOversell(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	for round := range 3 {
		b, _ := signedIn(t, h)
		p := b.mustCreatePortfolio("Long term", "")
		symbol := h.freshSymbol(t)
		day := h.tradeDays()
		b.mustCreateTrade(p.ID, h.trade(symbol, "buy", "5", day(1)))
		const sellers = 12
		browsers := tabs(t, h, b, sellers)
		sell := h.trade(symbol, "sell", "1", day(2))
		keys := make([]string, sellers)
		for i := range keys {
			keys[i] = newKey(t)
		}

		resps, bodies := raceAll(t, sellers, func(i int) (response, []byte, error) {
			return browsers[i].tryDo(nethttp.MethodPost, transactionsPath(p.ID), sell, map[string]string{"Idempotency-Key": keys[i]})
		})

		var created []string
		refused, busy := 0, 0
		for i, resp := range resps {
			switch {
			case resp.StatusCode == nethttp.StatusCreated:
				created = append(created, decodeTransaction(t, bodies[i]).ID)
			case resp.StatusCode == nethttp.StatusUnprocessableEntity && errorCode(t, bodies[i]) == "insufficient_shares":
				refused++
				if rec := h.idempotencyRecord(t, p.ID, keys[i]); rec.Found {
					t.Errorf("round %d: the refused sale %d kept its key", round, i)
				}
			case isServerBusy(t, resp, bodies[i]):
				busy++
			default:
				t.Errorf("round %d: sale %d = %d %s, want 201, 422 insufficient_shares or 503 server_busy", round, i, resp.StatusCode, bodies[i])
			}
		}
		if len(created) != 5 {
			t.Errorf("round %d: %d sales succeeded (%d refused, %d busy), want exactly 5", round, len(created), refused, busy)
		}
		if n := h.countRows(t, "transactions", p.ID); n != 1+len(created) {
			t.Errorf("round %d: transactions = %d, want the buy and %d sales", round, n, len(created))
		}
		if !h.historyNeverNegative(t, p.ID) {
			t.Errorf("round %d: the stored history goes below zero", round)
		}
		if q := h.heldQuantity(t, p.ID, symbol); q != "0" {
			t.Errorf("round %d: held = %s, want 0", round, q)
		}
		for _, id := range created {
			if events := h.auditByTarget(t, id); len(events) != 1 || events[0].EventType != "transaction.created" {
				t.Errorf("round %d: audit of sale %s = %v, want one transaction.created", round, id, eventTypes(events))
			}
		}
		if n := h.auditCount(t, "transaction.created", p.ID); n != 1+len(created) {
			t.Errorf("round %d: transaction.created events = %d, want %d", round, n, 1+len(created))
		}
		if n := h.countRows(t, "idempotency_keys", p.ID); n != 1+len(created) {
			t.Errorf("round %d: idempotency keys = %d, want one per stored trade", round, n)
		}
	}
}

func TestConcurrentRetriesOfOneKeyCreateOneTrade(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	for round := range 3 {
		b, _ := signedIn(t, h)
		p := b.mustCreatePortfolio("Long term", "")
		symbol := h.freshSymbol(t) // new, so every retry also races to verify and store it
		req := h.tradeRequest(symbol)
		key := newKey(t)
		const retries = 8
		browsers := tabs(t, h, b, retries)

		resps, bodies := raceAll(t, retries, func(i int) (response, []byte, error) {
			return browsers[i].tryDo(nethttp.MethodPost, transactionsPath(p.ID), req, map[string]string{"Idempotency-Key": key})
		})

		var first []byte
		ok := 0
		for i, resp := range resps {
			switch {
			case resp.StatusCode == nethttp.StatusCreated:
				ok++
				if first == nil {
					first = bodies[i]
				} else if !bytes.Equal(bodies[i], first) {
					t.Errorf("round %d: retry %d body\n%s\ndiffers from\n%s", round, i, bodies[i], first)
				}
				if v := resp.Header.Get("ETag"); v != `"1"` {
					t.Errorf("round %d: retry %d ETag = %q, want \"1\"", round, i, v)
				}
			case isServerBusy(t, resp, bodies[i]):
			default:
				t.Errorf("round %d: retry %d = %d %s, want 201 or 503 server_busy", round, i, resp.StatusCode, bodies[i])
			}
		}
		if ok == 0 {
			t.Fatalf("round %d: no retry succeeded", round)
		}
		created := decodeTransaction(t, first)
		if n := h.countRows(t, "transactions", p.ID); n != 1 {
			t.Errorf("round %d: transactions = %d, want 1", round, n)
		}
		if events := h.auditByTarget(t, created.ID); len(events) != 1 || events[0].EventType != "transaction.created" {
			t.Errorf("round %d: audit = %v, want one transaction.created", round, eventTypes(events))
		}
		if n := h.auditCount(t, "transaction.created", p.ID); n != 1 {
			t.Errorf("round %d: transaction.created events in the portfolio = %d, want 1", round, n)
		}
		if rec := h.idempotencyRecord(t, p.ID, key); !rec.Found || rec.ResultID != created.ID {
			t.Errorf("round %d: key record = %+v, want it to hold %s", round, rec, created.ID)
		}
		if !h.instrumentExists(t, symbol) {
			t.Errorf("round %d: instrument %s was not stored", round, symbol)
		}
	}
}

func TestConcurrentEditsWithOneVersionWinOnce(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	for round := range 3 {
		b, _ := signedIn(t, h)
		p := b.mustCreatePortfolio("Long term", "")
		symbol := h.freshSymbol(t)
		created := b.mustCreateTrade(p.ID, h.tradeRequest(symbol))
		browsers := tabs(t, h, b, 2)
		prices := []string{"301", "302"}
		path := transactionsPath(p.ID) + "/" + created.ID

		resps, bodies := raceAll(t, 2, func(i int) (response, []byte, error) {
			return browsers[i].tryDo(nethttp.MethodPut, path, h.replacement(symbol, prices[i]), map[string]string{"If-Match": `"1"`})
		})

		statuses := []int{resps[0].StatusCode, resps[1].StatusCode}
		slices.Sort(statuses)
		if !slices.Equal(statuses, []int{nethttp.StatusOK, nethttp.StatusPreconditionFailed}) {
			t.Fatalf("round %d: statuses = %v (%s / %s), want one 200 and one 412", round, statuses, bodies[0], bodies[1])
		}
		winner := 0
		if resps[1].StatusCode == nethttp.StatusOK {
			winner = 1
		}
		if code := errorCode(t, bodies[1-winner]); code != "version_conflict" {
			t.Errorf("round %d: loser code = %s, want version_conflict", round, code)
		}
		state := h.tradeState(t, created.ID)
		if state.Version != 2 || state.Price != prices[winner]+".0000000000" {
			t.Errorf("round %d: trade = %+v, want version 2 at the winner's price %s", round, state, prices[winner])
		}
		if got := decodeTransaction(t, bodies[winner]); got.Version != 2 || got.Price != prices[winner] {
			t.Errorf("round %d: winner's body = %+v", round, got)
		}
		updates := 0
		for _, e := range h.auditByTarget(t, created.ID) {
			if e.EventType == "transaction.updated" {
				updates++
			}
		}
		if updates != 1 {
			t.Errorf("round %d: transaction.updated events = %d, want 1", round, updates)
		}
	}
}

func TestConcurrentDeleteOfBuyAndSellLeaveAValidHistory(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	seen := map[string]int{}
	for round := range 6 {
		b, _ := signedIn(t, h)
		p := b.mustCreatePortfolio("Long term", "")
		symbol := h.freshSymbol(t)
		day := h.tradeDays()
		buy := b.mustCreateTrade(p.ID, h.trade(symbol, "buy", "5", day(1)))
		browsers := tabs(t, h, b, 2)
		key := newKey(t)

		resps, bodies := raceAll(t, 2, func(i int) (response, []byte, error) {
			if i == 0 {
				return browsers[0].tryDo(nethttp.MethodDelete, transactionsPath(p.ID)+"/"+buy.ID, nil, map[string]string{"If-Match": `"1"`})
			}
			return browsers[1].tryDo(nethttp.MethodPost, transactionsPath(p.ID), h.trade(symbol, "sell", "5", day(2)), map[string]string{"Idempotency-Key": key})
		})
		del, sell := resps[0], resps[1]

		rows := h.countRows(t, "transactions", p.ID)
		switch {
		case del.StatusCode == nethttp.StatusNoContent && sell.StatusCode == nethttp.StatusUnprocessableEntity && errorCode(t, bodies[1]) == "insufficient_shares":
			seen["delete first"]++
			if rows != 0 || h.idempotencyRecord(t, p.ID, key).Found {
				t.Errorf("round %d: the delete won but %d rows are left or the sale kept its key", round, rows)
			}
		case sell.StatusCode == nethttp.StatusCreated && del.StatusCode == nethttp.StatusUnprocessableEntity && errorCode(t, bodies[0]) == "invalid_history":
			seen["sale first"]++
			if rows != 2 || h.tradeState(t, buy.ID).Version != 1 {
				t.Errorf("round %d: the sale won but rows = %d, buy = %+v", round, rows, h.tradeState(t, buy.ID))
			}
		default:
			t.Errorf("round %d: DELETE buy = %d %s, POST sell = %d %s; want (204, 422 insufficient_shares) or (422 invalid_history, 201)",
				round, del.StatusCode, bodies[0], sell.StatusCode, bodies[1])
		}
		if !h.historyNeverNegative(t, p.ID) {
			t.Errorf("round %d: the stored history goes below zero", round)
		}
		if q := h.heldQuantity(t, p.ID, symbol); q != "0" {
			t.Errorf("round %d: held = %s, want 0 either way", round, q)
		}
	}
	t.Logf("outcomes: %v", seen)
}

func TestConcurrentCreatesOfANewSymbolShareOneInstrument(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	for round := range 3 {
		first, _ := signedIn(t, h)
		second, _ := signedIn(t, h)
		portfolios := []PortfolioResponse{first.mustCreatePortfolio("Long term", ""), second.mustCreatePortfolio("Long term", "")}
		browsers := []*browser{sameUserBrowser(t, h, first), sameUserBrowser(t, h, second)}
		symbol := h.freshSymbol(t)
		req := h.tradeRequest(symbol)

		resps, bodies := raceAll(t, 2, func(i int) (response, []byte, error) {
			return browsers[i].tryDo(nethttp.MethodPost, transactionsPath(portfolios[i].ID), req, map[string]string{"Idempotency-Key": "same-key"})
		})

		for i, resp := range resps {
			if resp.StatusCode != nethttp.StatusCreated {
				t.Fatalf("round %d: create %d = %d %s, want 201", round, i, resp.StatusCode, bodies[i])
			}
		}
		var stored int
		if err := h.admin.QueryRow(t.Context(), `SELECT count(*) FROM app.instruments WHERE symbol = $1`, symbol).Scan(&stored); err != nil || stored != 1 {
			t.Errorf("round %d: instruments of %s = %d, %v, want 1", round, symbol, stored, err)
		}
		instrument := h.instrumentID(t, symbol)
		for i := range resps {
			got := decodeTransaction(t, bodies[i])
			if state := h.tradeState(t, got.ID); state.InstrumentID != instrument || state.PortfolioID != portfolios[i].ID || got.Symbol != symbol {
				t.Errorf("round %d: trade %d = %+v, want instrument %s in its own portfolio", round, i, state, instrument)
			}
		}
	}
}

func TestConcurrentDeleteOfPortfolioAndTrade(t *testing.T) {
	h := newAuthHarness(t, withTransactions())
	known := h.freshSymbol(t)
	{
		b, _ := signedIn(t, h)
		b.mustCreateTrade(b.mustCreatePortfolio("Seed", "").ID, h.tradeRequest(known))
	}
	seen := map[string]int{}
	for round := range 6 {
		b, _ := signedIn(t, h)
		p := b.mustCreatePortfolio("Long term", "")
		browsers := tabs(t, h, b, 2)
		symbol := known
		if round%2 == 1 {
			symbol = h.freshSymbol(t) // a new symbol checks ownership before the lock as well
		}
		key := newKey(t)

		resps, bodies := raceAll(t, 2, func(i int) (response, []byte, error) {
			if i == 0 {
				return browsers[0].tryDo(nethttp.MethodDelete, "/api/v1/portfolios/"+p.ID, nil, map[string]string{"If-Match": `"1"`})
			}
			return browsers[1].tryDo(nethttp.MethodPost, transactionsPath(p.ID), h.tradeRequest(symbol), map[string]string{"Idempotency-Key": key})
		})
		del, create := resps[0], resps[1]

		state := h.portfolioState(t, p.ID)
		rows := h.countRows(t, "transactions", p.ID)
		keys := h.countRows(t, "idempotency_keys", p.ID)
		switch {
		case del.StatusCode == nethttp.StatusNoContent && create.StatusCode == nethttp.StatusNotFound && errorCode(t, bodies[1]) == "not_found":
			seen["delete first"]++
			if state.Found || rows != 0 || keys != 0 {
				t.Errorf("round %d: the delete won but portfolio = %+v, rows = %d, keys = %d", round, state, rows, keys)
			}
		case create.StatusCode == nethttp.StatusCreated && del.StatusCode == nethttp.StatusConflict && errorCode(t, bodies[0]) == "portfolio_not_empty":
			seen["trade first"]++
			if !state.Found || state.Version != 1 || rows != 1 || keys != 1 {
				t.Errorf("round %d: the trade won but portfolio = %+v, rows = %d, keys = %d", round, state, rows, keys)
			}
		default:
			t.Errorf("round %d: DELETE portfolio = %d %s, POST trade = %d %s; want (204, 404) or (409, 201)",
				round, del.StatusCode, bodies[0], create.StatusCode, bodies[1])
		}
		if denied := accessDenied(h.auditByTarget(t, p.ID)); len(denied) != 0 {
			t.Errorf("round %d: access_denied events for the owner's own portfolio: %+v", round, denied)
		}
	}
	t.Logf("outcomes: %v", seen)
}
