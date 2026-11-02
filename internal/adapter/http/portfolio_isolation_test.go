package http

import (
	"encoding/json"
	nethttp "net/http"
	"slices"
	"sync"
	"testing"
)

// accessDenied returns the access-denied events of a target.
func accessDenied(rows []auditRow) []auditRow {
	var out []auditRow
	for _, r := range rows {
		if r.EventType == "security.access_denied" {
			out = append(out, r)
		}
	}
	return out
}

func TestAnotherUsersPortfolioLooksMissing(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	owner, ownerUser := signedIn(t, h)
	intruder, intruderUser := signedIn(t, h)
	mine := owner.mustCreatePortfolio("Long term", "private")
	unknown := mustTestID(t).String()

	attempts := map[string]func(id string) (response, []byte){
		"GET":    func(id string) (response, []byte) { return intruder.getPortfolio(id) },
		"PUT":    func(id string) (response, []byte) { return intruder.updatePortfolio(id, `"1"`, "Taken over", "") },
		"DELETE": func(id string) (response, []byte) { return intruder.deletePortfolio(id, `"1"`) },
	}
	for method, attempt := range attempts {
		foreignResp, foreignBody := attempt(mine.ID)
		unknownResp, unknownBody := attempt(unknown)
		if foreignResp.StatusCode != nethttp.StatusNotFound || unknownResp.StatusCode != nethttp.StatusNotFound {
			t.Errorf("%s: another user's portfolio = %d, unknown portfolio = %d, want 404 for both", method, foreignResp.StatusCode, unknownResp.StatusCode)
		}
		if a, b := stripJSONField(t, foreignBody, "error", "request_id"), stripJSONField(t, unknownBody, "error", "request_id"); a != b {
			t.Errorf("%s: the two 404 bodies differ:\n%s\n%s", method, a, b)
		}
	}

	if state := h.portfolioState(t, mine.ID); state.Name != "Long term" || state.Version != 1 {
		t.Errorf("owner's portfolio = %+v, want it untouched", state)
	}
	denied := accessDenied(h.auditByTarget(t, mine.ID))
	if len(denied) != 2 {
		t.Fatalf("access_denied events = %d (%+v), want one for PUT and one for DELETE", len(denied), denied)
	}
	for _, e := range denied {
		if e.Actor != intruderUser.ID || e.Reason != "not_owner" || e.Outcome != "denied" {
			t.Errorf("access_denied = %+v, want actor %s, reason not_owner, outcome denied", e, intruderUser.ID)
		}
	}
	if got := h.auditByTarget(t, unknown); len(got) != 0 {
		t.Errorf("audit events for an ID nobody owns = %+v, want none", got)
	}
	for _, e := range h.auditByTarget(t, mine.ID) {
		if e.EventType == "portfolio.updated" || e.EventType == "portfolio.deleted" {
			t.Errorf("audit has %s for a refused change by %s (owner is %s)", e.EventType, intruderUser.ID, ownerUser.ID)
		}
	}
	if resp, body := intruder.listPortfolios(""); resp.StatusCode != nethttp.StatusOK || len(decodePortfolioPage(t, body).Data) != 0 {
		t.Errorf("the intruder's list = %d %s, want it empty", resp.StatusCode, body)
	}
}

func TestOneUserManyPortfoliosAreSeparate(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	long := b.mustCreatePortfolio("ระยะยาว", "")
	short := b.mustCreatePortfolio("ระยะสั้น", "")
	spec := b.mustCreatePortfolio("เก็งกำไร", "")

	if resp, body := b.updatePortfolio(short.ID, `"1"`, "ระยะสั้นมาก", "renamed"); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, body)
	}
	for _, p := range []PortfolioResponse{long, spec} {
		if state := h.portfolioState(t, p.ID); state.Name != p.Name || state.Version != 1 {
			t.Errorf("portfolio %q = %+v, want it untouched by the other portfolio's update", p.Name, state)
		}
	}

	if resp, body := b.deletePortfolio(spec.ID, `"1"`); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("delete = %d %s", resp.StatusCode, body)
	}
	resp, body := b.listPortfolios("")
	page := decodePortfolioPage(t, body)
	var names []string
	for _, p := range page.Data {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	if resp.StatusCode != nethttp.StatusOK || !slices.Equal(names, []string{"ระยะยาว", "ระยะสั้นมาก"}) {
		t.Errorf("list = %d %v, want the two portfolios that are left", resp.StatusCode, names)
	}
}

// sameUserBrowser returns another browser holding the session of b, as a second tab or device would.
func sameUserBrowser(t *testing.T, h *authHarness, b *browser) *browser {
	t.Helper()
	other := h.newBrowser(t)
	other.jar["session"] = b.jar["session"]
	other.fetchCSRF()
	return other
}

// raceTwo runs both requests at the same moment, after both goroutines exist.
func raceTwo(t *testing.T, first, second func() (response, []byte, error)) (response, response) {
	t.Helper()
	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		results [2]response
		errs    [2]error
	)
	for i, request := range []func() (response, []byte, error){first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], _, errs[i] = request()
		}()
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
	}
	return results[0], results[1]
}

func TestConcurrentPortfolioUpdatesWithOneVersionWinOnce(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	for round := range 3 {
		b, _ := signedIn(t, h)
		c := sameUserBrowser(t, h, b)
		created := b.mustCreatePortfolio("Long term", "")
		b.fetchCSRF()
		path := "/api/v1/portfolios/" + created.ID

		a, bb := raceTwo(t,
			func() (response, []byte, error) {
				return b.tryDo(nethttp.MethodPut, path, map[string]string{"name": "From tab A"}, map[string]string{"If-Match": `"1"`})
			},
			func() (response, []byte, error) {
				return c.tryDo(nethttp.MethodPut, path, map[string]string{"name": "From tab B"}, map[string]string{"If-Match": `"1"`})
			})

		statuses := []int{a.StatusCode, bb.StatusCode}
		slices.Sort(statuses)
		if !slices.Equal(statuses, []int{nethttp.StatusOK, nethttp.StatusPreconditionFailed}) {
			t.Fatalf("round %d: statuses = %v, want one 200 and one 412", round, statuses)
		}
		winner := "From tab A"
		if bb.StatusCode == nethttp.StatusOK {
			winner = "From tab B"
		}
		if state := h.portfolioState(t, created.ID); state.Version != 2 || state.Name != winner {
			t.Errorf("round %d: portfolio = %+v, want version 2 named %q", round, state, winner)
		}
		updates := 0
		for _, e := range h.auditByTarget(t, created.ID) {
			if e.EventType == "portfolio.updated" {
				updates++
			}
		}
		if updates != 1 {
			t.Errorf("round %d: portfolio.updated events = %d, want 1", round, updates)
		}
	}
}

func TestConcurrentDeleteAndUpdateLeaveOneOutcome(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	for round := range 5 {
		b, _ := signedIn(t, h)
		c := sameUserBrowser(t, h, b)
		created := b.mustCreatePortfolio("Long term", "")
		b.fetchCSRF()
		path := "/api/v1/portfolios/" + created.ID

		del, put := raceTwo(t,
			func() (response, []byte, error) {
				return b.tryDo(nethttp.MethodDelete, path, nil, map[string]string{"If-Match": `"1"`})
			},
			func() (response, []byte, error) {
				return c.tryDo(nethttp.MethodPut, path, map[string]string{"name": "Renamed"}, map[string]string{"If-Match": `"1"`})
			})

		state := h.portfolioState(t, created.ID)
		switch {
		case del.StatusCode == nethttp.StatusNoContent && put.StatusCode == nethttp.StatusNotFound:
			if state.Found {
				t.Errorf("round %d: DELETE won but the portfolio still exists: %+v", round, state)
			}
		case put.StatusCode == nethttp.StatusOK && del.StatusCode == nethttp.StatusPreconditionFailed:
			if !state.Found || state.Version != 2 || state.Name != "Renamed" {
				t.Errorf("round %d: PUT won but the portfolio is %+v, want Renamed at version 2", round, state)
			}
		default:
			t.Errorf("round %d: DELETE = %d, PUT = %d, want (204, 404) or (412, 200)", round, del.StatusCode, put.StatusCode)
		}
		if denied := accessDenied(h.auditByTarget(t, created.ID)); len(denied) != 0 {
			t.Errorf("round %d: access_denied events for the owner's own portfolio: %+v", round, denied)
		}
	}
}

func TestPortfolioChangesWithoutTheirAuditChangeNothing(t *testing.T) {
	healthy := newAuthHarness(t, withPortfolios())
	failing := newAuthHarness(t, withPortfolios(), withFailingAudit())
	b, user := signedIn(t, healthy)
	mine := b.mustCreatePortfolio("Long term", "")

	// The same session, served by an API whose audit store is down.
	c := sameUserBrowser(t, failing, b)

	resp, body := c.createPortfolio("Never stored", "")
	if resp.StatusCode != nethttp.StatusInternalServerError || errorCode(t, body) != "audit_write_failed" {
		t.Errorf("create = %d %s, want 500 audit_write_failed", resp.StatusCode, body)
	}
	var stored int
	if err := healthy.admin.QueryRow(t.Context(), `SELECT count(*) FROM app.portfolios WHERE user_id = $1 AND name = 'Never stored'`, user.ID).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("portfolios named 'Never stored' = %d, %v, want 0", stored, err)
	}

	resp, body = c.updatePortfolio(mine.ID, `"1"`, "Renamed", "")
	if resp.StatusCode != nethttp.StatusInternalServerError || errorCode(t, body) != "audit_write_failed" {
		t.Errorf("update = %d %s, want 500 audit_write_failed", resp.StatusCode, body)
	}
	resp, body = c.deletePortfolio(mine.ID, `"1"`)
	if resp.StatusCode != nethttp.StatusInternalServerError || errorCode(t, body) != "audit_write_failed" {
		t.Errorf("delete = %d %s, want 500 audit_write_failed", resp.StatusCode, body)
	}
	if state := healthy.portfolioState(t, mine.ID); !state.Found || state.Name != "Long term" || state.Version != 1 {
		t.Errorf("portfolio after the failed changes = %+v, want it untouched", state)
	}
}

func TestLoggedOutSessionCannotUsePortfolios(t *testing.T) {
	h := newAuthHarness(t, withPortfolios())
	b, _ := signedIn(t, h)
	b.mustCreatePortfolio("Long term", "")
	oldSession := b.jar["session"]

	if resp, body := b.logout(); resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("logout = %d %s", resp.StatusCode, body)
	}
	b.jar["session"] = oldSession
	resp, body := b.listPortfolios("")
	if resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("list with a revoked session = %d %s, want 401", resp.StatusCode, body)
	}
	var out ErrorResponse
	if err := json.Unmarshal(body, &out); err != nil || out.Error.Code != "unauthenticated" {
		t.Errorf("body = %s, want unauthenticated", body)
	}
}
