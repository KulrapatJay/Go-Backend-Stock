package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// callLog is the order in which a use case touched the repositories.
type callLog struct{ calls []string }

func (l *callLog) add(call string) { l.calls = append(l.calls, call) }

type fakePortfolios struct {
	port.PortfolioRepository
	log         *callLog
	stored      map[domain.ID]domain.Portfolio
	insertErr   error
	existsAnswe bool
	inserted    []domain.Portfolio
	updated     []domain.Portfolio
}

func (f *fakePortfolios) Insert(_ context.Context, p domain.Portfolio) error {
	f.log.add("Portfolios.Insert")
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, p)
	return nil
}

func (f *fakePortfolios) LockOwned(_ context.Context, ownerID, id domain.ID) (domain.Portfolio, error) {
	f.log.add("Portfolios.LockOwned")
	p, ok := f.stored[id]
	if !ok || p.UserID != ownerID {
		return domain.Portfolio{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *fakePortfolios) UpdateOwned(_ context.Context, p domain.Portfolio, _ domain.ID) (domain.Portfolio, error) {
	f.log.add("Portfolios.UpdateOwned")
	f.updated = append(f.updated, p)
	v, err := domain.ParseVersion(int64(p.Version.Int32()) + 1)
	if err != nil {
		return domain.Portfolio{}, err
	}
	p.Version = v
	f.stored[p.ID] = p
	return p, nil
}

func (f *fakePortfolios) DeleteOwnedEmpty(context.Context, domain.ID, domain.ID) error {
	f.log.add("Portfolios.DeleteOwnedEmpty")
	return nil
}

func (f *fakePortfolios) Exists(context.Context, domain.ID) (bool, error) {
	f.log.add("Portfolios.Exists")
	return f.existsAnswe, nil
}

type fakePortfolioKeys struct {
	port.IdempotencyRepository
	log *callLog
}

func (f *fakePortfolioKeys) DeleteForPortfolio(context.Context, domain.ID, domain.ID) error {
	f.log.add("Idempotency.DeleteForPortfolio")
	return nil
}

type loggingAudit struct {
	log   *callLog
	audit *fakeAudit
}

func (a loggingAudit) Append(ctx context.Context, e domain.AuditEvent) error {
	a.log.add("Audit.Append")
	return a.audit.Append(ctx, e)
}

type portfolioStore struct {
	port.Repositories
	portfolios *fakePortfolios
}

func (s portfolioStore) Portfolios() port.PortfolioRepository { return s.portfolios }

type portfolioTx struct {
	port.TxRepositories
	portfolios *fakePortfolios
	keys       *fakePortfolioKeys
	audit      loggingAudit
}

func (t portfolioTx) Portfolios() port.PortfolioRepository { return t.portfolios }
func (t portfolioTx) Idempotency() port.IdempotencyRepository {
	return t.keys
}
func (t portfolioTx) Audit() port.AuditRepository { return t.audit }

type portfolioUoW struct {
	tx   portfolioTx
	runs int
}

func (u *portfolioUoW) WithinTransaction(_ context.Context, fn func(port.TxRepositories) error) error {
	u.runs++
	return fn(u.tx)
}

type testPortfolios struct {
	svc        *Portfolios
	repo       *fakePortfolios
	audit      *fakeAudit
	uow        *portfolioUoW
	clock      *fakeClock
	log        *callLog
	caller     domain.Principal
	callerMeta domain.RequestMeta
}

func newTestPortfolios(t *testing.T) testPortfolios {
	t.Helper()
	log := &callLog{}
	repo := &fakePortfolios{log: log, stored: map[domain.ID]domain.Portfolio{}}
	audit := &fakeAudit{}
	uow := &portfolioUoW{tx: portfolioTx{portfolios: repo, keys: &fakePortfolioKeys{log: log}, audit: loggingAudit{log: log, audit: audit}}}
	clock := newFakeClock()
	svc, err := NewPortfolios(PortfolioDeps{
		Store: portfolioStore{portfolios: repo}, UnitOfWork: uow, Clock: clock, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewPortfolios() error = %v", err)
	}
	return testPortfolios{
		svc: svc, repo: repo, audit: audit, uow: uow, clock: clock, log: log,
		caller:     domain.Principal{UserID: mustID(t), SessionID: mustID(t)},
		callerMeta: testMeta(t),
	}
}

func (tp testPortfolios) seed(t *testing.T, owner domain.ID, name string, version int64) domain.Portfolio {
	t.Helper()
	in, err := domain.ParsePortfolioInput(name, "first description")
	if err != nil {
		t.Fatal(err)
	}
	v, err := domain.ParseVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.Portfolio{
		ID: mustID(t), UserID: owner, Name: in.Name, Description: in.Description, Currency: domain.CurrencyUSD,
		Version: v, CreatedAt: tp.clock.Now(), UpdatedAt: tp.clock.Now(),
	}
	tp.repo.stored[p.ID] = p
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

func mustPortfolioInput(t *testing.T, name, description string) domain.PortfolioInput {
	t.Helper()
	in, err := domain.ParsePortfolioInput(name, description)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func auditMetadataOf(t *testing.T, e domain.AuditEvent) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(e.Metadata, &m); err != nil {
		t.Fatalf("metadata %s: %v", e.Metadata, err)
	}
	return m
}

func TestCreatePortfolioWritesItsAudit(t *testing.T) {
	tp := newTestPortfolios(t)

	got, err := tp.svc.Create(t.Context(), tp.caller, mustPortfolioInput(t, "Long term", ""), tp.callerMeta)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.UserID != tp.caller.UserID || got.Version.Int32() != 1 || got.Currency != domain.CurrencyUSD ||
		got.Name.String() != "Long term" || !got.CreatedAt.Equal(got.UpdatedAt) || got.ID.IsZero() {
		t.Errorf("Create() = %+v", got)
	}
	if len(tp.repo.inserted) != 1 || tp.repo.inserted[0].ID != got.ID {
		t.Errorf("inserted = %+v, want the returned portfolio", tp.repo.inserted)
	}
	if len(tp.audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(tp.audit.events))
	}
	e := tp.audit.events[0]
	if e.EventType != domain.EventPortfolioCreated || e.Outcome != domain.AuditOutcomeSuccess ||
		e.ActorUserID != tp.caller.UserID || e.SessionID != tp.caller.SessionID ||
		e.TargetType != "portfolio" || e.TargetID != got.ID.String() || e.RequestID != tp.callerMeta.RequestID {
		t.Errorf("audit event = %+v", e)
	}
	if m := auditMetadataOf(t, e); m["ip"] != tp.callerMeta.MaskedIP || m["user_agent"] != tp.callerMeta.UserAgent || len(m) != 2 {
		t.Errorf("audit metadata = %v, want only ip and user_agent", m)
	}
}

func TestCreatePortfolioNameConflictWritesNoAudit(t *testing.T) {
	tp := newTestPortfolios(t)
	tp.repo.insertErr = domain.ErrPortfolioNameConflict

	_, err := tp.svc.Create(t.Context(), tp.caller, mustPortfolioInput(t, "Long term", ""), tp.callerMeta)
	if !errors.Is(err, domain.ErrPortfolioNameConflict) {
		t.Fatalf("Create() error = %v, want portfolio_name_conflict", err)
	}
	if len(tp.audit.events) != 0 {
		t.Errorf("audit events = %d, want none", len(tp.audit.events))
	}
}

func TestPortfolioUseCasesNeedACaller(t *testing.T) {
	tp := newTestPortfolios(t)
	id, in, v := mustID(t), mustPortfolioInput(t, "A", ""), mustVersion(t, 1)
	page, err := domain.ParsePage("", "")
	if err != nil {
		t.Fatal(err)
	}
	var nobody domain.Principal

	_, errCreate := tp.svc.Create(t.Context(), nobody, in, tp.callerMeta)
	_, errList := tp.svc.List(t.Context(), nobody, page)
	_, errGet := tp.svc.Get(t.Context(), nobody, id)
	_, errUpdate := tp.svc.Update(t.Context(), nobody, id, v, in, tp.callerMeta)
	errDelete := tp.svc.Delete(t.Context(), nobody, id, v, tp.callerMeta)

	for name, err := range map[string]error{"Create": errCreate, "List": errList, "Get": errGet, "Update": errUpdate, "Delete": errDelete} {
		if !errors.Is(err, domain.ErrUnauthenticated) {
			t.Errorf("%s(no caller) error = %v, want unauthenticated", name, err)
		}
	}
	if len(tp.log.calls) != 0 || tp.uow.runs != 0 {
		t.Errorf("repositories touched: %v (%d transactions)", tp.log.calls, tp.uow.runs)
	}
}

func TestUpdatePortfolioChecksTheVersionUnderTheLock(t *testing.T) {
	tp := newTestPortfolios(t)
	p := tp.seed(t, tp.caller.UserID, "Long term", 3)

	_, err := tp.svc.Update(t.Context(), tp.caller, p.ID, mustVersion(t, 2), mustPortfolioInput(t, "Renamed", ""), tp.callerMeta)
	if !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("Update(stale version) error = %v, want version_conflict", err)
	}
	if want := []string{"Portfolios.LockOwned"}; !slices.Equal(tp.log.calls, want) {
		t.Errorf("calls = %v, want %v", tp.log.calls, want)
	}
	if len(tp.audit.events) != 0 {
		t.Errorf("audit events = %d, want none", len(tp.audit.events))
	}
}

func TestUpdatePortfolioAuditsTheChangedFields(t *testing.T) {
	tp := newTestPortfolios(t)
	p := tp.seed(t, tp.caller.UserID, "Long term", 2)
	tp.clock.Advance(time.Minute)

	got, err := tp.svc.Update(t.Context(), tp.caller, p.ID, mustVersion(t, 2), mustPortfolioInput(t, "Long term", "new text"), tp.callerMeta)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got.Version.Int32() != 3 || got.Description.String() != "new text" || !got.UpdatedAt.Equal(tp.clock.Now()) || !got.CreatedAt.Equal(p.CreatedAt) {
		t.Errorf("Update() = %+v", got)
	}
	if len(tp.repo.updated) != 1 || tp.repo.updated[0].Version.Int32() != 2 {
		t.Errorf("UpdateOwned got %+v, want the version the client read (2)", tp.repo.updated)
	}
	e := tp.audit.events[len(tp.audit.events)-1]
	if e.EventType != domain.EventPortfolioUpdated || e.TargetID != p.ID.String() || e.ActorUserID != tp.caller.UserID {
		t.Errorf("audit event = %+v", e)
	}
	if m := auditMetadataOf(t, e); !slices.Equal(anyStrings(m["changed"]), []string{"description"}) {
		t.Errorf("changed = %v, want [description]", m["changed"])
	}

	// Sending the same values again changes nothing, but it is still an update that is recorded.
	same := tp.repo.stored[p.ID]
	if _, err := tp.svc.Update(t.Context(), tp.caller, p.ID, same.Version, mustPortfolioInput(t, same.Name.String(), same.Description.String()), tp.callerMeta); err != nil {
		t.Fatalf("Update(same values) error = %v", err)
	}
	e = tp.audit.events[len(tp.audit.events)-1]
	if _, has := auditMetadataOf(t, e)["changed"]; has || e.EventType != domain.EventPortfolioUpdated {
		t.Errorf("unchanged update event = %+v, want portfolio.updated without changed", e)
	}
}

func TestDeletePortfolioRemovesIdempotencyKeysFirst(t *testing.T) {
	tp := newTestPortfolios(t)
	p := tp.seed(t, tp.caller.UserID, "Long term", 1)

	if err := tp.svc.Delete(t.Context(), tp.caller, p.ID, mustVersion(t, 1), tp.callerMeta); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	want := []string{"Portfolios.LockOwned", "Idempotency.DeleteForPortfolio", "Portfolios.DeleteOwnedEmpty", "Audit.Append"}
	if !slices.Equal(tp.log.calls, want) {
		t.Errorf("calls = %v, want %v", tp.log.calls, want)
	}
	if e := tp.audit.events[0]; e.EventType != domain.EventPortfolioDeleted || e.TargetID != p.ID.String() {
		t.Errorf("audit event = %+v", e)
	}
}

func TestUpdateOrDeleteOfAnotherUsersPortfolioIsDeniedAndAudited(t *testing.T) {
	someoneElse := mustID(t)
	cases := map[string]func(tp testPortfolios, id domain.ID) error{
		"update": func(tp testPortfolios, id domain.ID) error {
			_, err := tp.svc.Update(t.Context(), tp.caller, id, mustVersion(t, 1), mustPortfolioInput(t, "Mine now", ""), tp.callerMeta)
			return err
		},
		"delete": func(tp testPortfolios, id domain.ID) error {
			return tp.svc.Delete(t.Context(), tp.caller, id, mustVersion(t, 1), tp.callerMeta)
		},
	}
	for name, call := range cases {
		t.Run(name+" of a portfolio that exists", func(t *testing.T) {
			tp := newTestPortfolios(t)
			other := tp.seed(t, someoneElse, "Theirs", 1)
			tp.repo.existsAnswe = true

			if err := call(tp, other.ID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("error = %v, want not_found", err)
			}
			if len(tp.audit.events) != 1 {
				t.Fatalf("audit events = %d, want 1", len(tp.audit.events))
			}
			e := tp.audit.events[0]
			if e.EventType != domain.EventSecurityAccessDenied || e.Outcome != domain.AuditOutcomeDenied || e.ReasonCode != "not_owner" ||
				e.ActorUserID != tp.caller.UserID || e.SessionID != tp.caller.SessionID ||
				e.TargetType != "portfolio" || e.TargetID != other.ID.String() {
				t.Errorf("audit event = %+v", e)
			}
			if len(tp.repo.updated) != 0 {
				t.Errorf("the other user's portfolio was written: %+v", tp.repo.updated)
			}
		})
		t.Run(name+" of a portfolio that does not exist", func(t *testing.T) {
			tp := newTestPortfolios(t)

			if err := call(tp, mustID(t)); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("error = %v, want not_found", err)
			}
			if len(tp.audit.events) != 0 {
				t.Errorf("audit events = %d, want none for an unknown ID", len(tp.audit.events))
			}
		})
	}
}

func anyStrings(v any) []string {
	items, _ := v.([]any)
	var out []string
	for _, item := range items {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}
