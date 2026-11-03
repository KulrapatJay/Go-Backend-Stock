package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// GetOwned is the ownership check a use case makes outside a transaction; it reads like LockOwned
// without the lock.
func (f *fakePortfolios) GetOwned(_ context.Context, ownerID, id domain.ID) (domain.Portfolio, error) {
	f.log.add("Portfolios.GetOwned")
	p, ok := f.stored[id]
	if !ok || p.UserID != ownerID {
		return domain.Portfolio{}, domain.ErrNotFound
	}
	return p, nil
}

// fakeTransactions behaves like the database: Insert assigns the next sequence and version 1,
// Update matches the current version and returns version + 1 with the stored sequence and creation
// time, and History returns every stored row ordered by (executed_at, sequence). Like the SQL, every
// query reaches only portfolios of ownerID; another owner's portfolio is not found.
type fakeTransactions struct {
	port.TransactionRepository
	log        *callLog
	portfolios *fakePortfolios
	rows       []domain.Transaction
	nextSeq    int64
	inserted   []domain.Transaction // what the service passed to Insert
	updated    []domain.Transaction // what the service passed to Update
	deleted    []domain.ID
	filters    []domain.TransactionFilter // what the service passed to List
	insertErr  error
	// stored, when set, changes the row Insert returns, as a database default or trigger would.
	stored func(*domain.Transaction)
}

func (f *fakeTransactions) owned(ownerID, portfolioID domain.ID) bool {
	p, ok := f.portfolios.stored[portfolioID]
	return ok && p.UserID == ownerID
}

// find returns the index of the trade in rows, or -1.
func (f *fakeTransactions) find(ownerID, portfolioID, id domain.ID) int {
	if !f.owned(ownerID, portfolioID) {
		return -1
	}
	return slices.IndexFunc(f.rows, func(t domain.Transaction) bool { return t.ID == id && t.PortfolioID == portfolioID })
}

func (f *fakeTransactions) Insert(_ context.Context, _ domain.ID, t domain.Transaction) (domain.Transaction, error) {
	f.log.add("Transactions.Insert")
	f.inserted = append(f.inserted, t)
	if f.insertErr != nil {
		return domain.Transaction{}, f.insertErr
	}
	f.nextSeq++
	t.Sequence = f.nextSeq
	if f.stored != nil {
		f.stored(&t)
	}
	f.rows = append(f.rows, t)
	return t, nil
}

func (f *fakeTransactions) Get(_ context.Context, ownerID, portfolioID, id domain.ID) (domain.Transaction, error) {
	f.log.add("Transactions.Get")
	i := f.find(ownerID, portfolioID, id)
	if i < 0 {
		return domain.Transaction{}, domain.ErrNotFound
	}
	return f.rows[i], nil
}

func (f *fakeTransactions) List(_ context.Context, ownerID, portfolioID domain.ID, filter domain.TransactionFilter) (domain.TransactionPage, error) {
	f.log.add("Transactions.List")
	f.filters = append(f.filters, filter)
	if !f.owned(ownerID, portfolioID) {
		return domain.TransactionPage{}, domain.ErrNotFound
	}
	var page domain.TransactionPage
	for _, t := range f.rows {
		if t.PortfolioID == portfolioID {
			page.Items = append(page.Items, t)
		}
	}
	slices.Reverse(page.Items)
	return page, nil
}

func (f *fakeTransactions) Update(_ context.Context, ownerID domain.ID, t domain.Transaction) (domain.Transaction, error) {
	f.log.add("Transactions.Update")
	f.updated = append(f.updated, t)
	i := f.find(ownerID, t.PortfolioID, t.ID)
	if i < 0 {
		return domain.Transaction{}, domain.ErrNotFound
	}
	current := f.rows[i]
	if current.Version != t.Version {
		return domain.Transaction{}, domain.ErrVersionConflict
	}
	next, err := domain.ParseVersion(int64(current.Version.Int32()) + 1)
	if err != nil {
		return domain.Transaction{}, err
	}
	t.Version, t.Sequence, t.CreatedAt = next, current.Sequence, current.CreatedAt
	f.rows[i] = t
	return t, nil
}

func (f *fakeTransactions) Delete(_ context.Context, ownerID, portfolioID, id domain.ID) error {
	f.log.add("Transactions.Delete")
	f.deleted = append(f.deleted, id)
	i := f.find(ownerID, portfolioID, id)
	if i < 0 {
		return domain.ErrNotFound
	}
	f.rows = slices.Delete(f.rows, i, i+1)
	return nil
}

func (f *fakeTransactions) History(_ context.Context, ownerID, portfolioID domain.ID) ([]domain.Transaction, error) {
	f.log.add("Transactions.History")
	if !f.owned(ownerID, portfolioID) {
		return nil, domain.ErrNotFound
	}
	var out []domain.Transaction
	for _, t := range f.rows {
		if t.PortfolioID == portfolioID {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b domain.Transaction) int {
		if c := a.ExecutedAt.Compare(b.ExecutedAt); c != 0 {
			return c
		}
		return int(a.Sequence - b.Sequence)
	})
	return out, nil
}

// fakeInstruments is the shared instruments table. Upsert returns raceWinner when it is set, as
// when a concurrent request stored the symbol first.
type fakeInstruments struct {
	port.InstrumentRepository
	log        *callLog
	bySymbol   map[string]domain.Instrument
	upserted   []domain.Instrument
	raceWinner *domain.Instrument
}

func (f *fakeInstruments) BySymbol(_ context.Context, s domain.Symbol) (domain.Instrument, error) {
	f.log.add("Instruments.BySymbol")
	i, ok := f.bySymbol[s.String()]
	if !ok {
		return domain.Instrument{}, domain.ErrNotFound
	}
	return i, nil
}

func (f *fakeInstruments) Upsert(_ context.Context, i domain.Instrument) (domain.Instrument, error) {
	f.log.add("Instruments.Upsert")
	f.upserted = append(f.upserted, i)
	if f.raceWinner != nil {
		return *f.raceWinner, nil
	}
	f.bySymbol[i.Symbol.String()] = i
	return i, nil
}

// fakeTransactionKeys records the scope and key of every lookup and deletion, so a test can check
// that the key is looked up for the right user, portfolio and operation.
type fakeTransactionKeys struct {
	port.IdempotencyRepository
	log          *callLog
	record       *domain.IdempotencyRecord
	gotScope     domain.IdempotencyScope
	gotKey       string
	deleted      []time.Time
	deletedScope domain.IdempotencyScope
	deletedKey   string
	saved        []domain.IdempotencyRecord
}

func (f *fakeTransactionKeys) Get(_ context.Context, scope domain.IdempotencyScope, key string) (domain.IdempotencyRecord, error) {
	f.log.add("Idempotency.Get")
	f.gotScope, f.gotKey = scope, key
	if f.record == nil {
		return domain.IdempotencyRecord{}, domain.ErrNotFound
	}
	return *f.record, nil
}

func (f *fakeTransactionKeys) DeleteExpired(_ context.Context, scope domain.IdempotencyScope, key string, now time.Time) error {
	f.log.add("Idempotency.DeleteExpired")
	f.deleted = append(f.deleted, now)
	f.deletedScope, f.deletedKey = scope, key
	return nil
}

func (f *fakeTransactionKeys) Save(_ context.Context, r domain.IdempotencyRecord) error {
	f.log.add("Idempotency.Save")
	f.saved = append(f.saved, r)
	return nil
}

// loggingLimits records each Admit in the call log and the scopes it was asked about.
type loggingLimits struct {
	log    *callLog
	limits *fakeRateLimits
	scopes *[]domain.RateLimitScope
}

func (l loggingLimits) Admit(ctx context.Context, rules []domain.RateLimitRule, now time.Time) (domain.RateLimitDecision, error) {
	l.log.add("RateLimits.Admit")
	for _, r := range rules {
		*l.scopes = append(*l.scopes, r.Scope)
	}
	return l.limits.Admit(ctx, rules, now)
}

// fakeProvider records how many transactions had started when it was called: the provider must be
// asked before the portfolio lock is taken. It answers for the symbol asked, or for renamedTo when
// that is set, as a provider that followed a ticker change would.
type fakeProvider struct {
	log        *callLog
	uow        *transactionUoW
	answer     domain.Instrument
	renamedTo  *domain.Symbol
	err        error
	runsAtCall []int
}

func (f *fakeProvider) Lookup(_ context.Context, s domain.Symbol) (domain.Instrument, error) {
	f.log.add("Provider.Lookup")
	f.runsAtCall = append(f.runsAtCall, f.uow.runs)
	if f.err != nil {
		return domain.Instrument{}, f.err
	}
	answer := f.answer
	answer.Symbol = s
	if f.renamedTo != nil {
		answer.Symbol = *f.renamedTo
	}
	return answer, nil
}

type transactionStore struct {
	port.Repositories
	portfolios   *fakePortfolios
	transactions *fakeTransactions
	instruments  *fakeInstruments
	limits       loggingLimits
}

func (s transactionStore) Portfolios() port.PortfolioRepository     { return s.portfolios }
func (s transactionStore) Transactions() port.TransactionRepository { return s.transactions }
func (s transactionStore) Instruments() port.InstrumentRepository   { return s.instruments }
func (s transactionStore) RateLimits() port.RateLimitRepository     { return s.limits }

type transactionTx struct {
	port.TxRepositories
	portfolios   *fakePortfolios
	transactions *fakeTransactions
	keys         *fakeTransactionKeys
	instruments  *fakeInstruments
	audit        loggingAudit
}

func (t transactionTx) Portfolios() port.PortfolioRepository     { return t.portfolios }
func (t transactionTx) Transactions() port.TransactionRepository { return t.transactions }
func (t transactionTx) Idempotency() port.IdempotencyRepository  { return t.keys }
func (t transactionTx) Instruments() port.InstrumentRepository   { return t.instruments }
func (t transactionTx) Audit() port.AuditRepository              { return t.audit }

// transactionUoW records what each transaction ended with: nil commits, an error rolls back. The
// fakes do not undo writes, so a test of a refused change checks that nothing was committed.
type transactionUoW struct {
	tx      transactionTx
	runs    int
	results []error
}

func (u *transactionUoW) WithinTransaction(_ context.Context, fn func(port.TxRepositories) error) error {
	u.runs++
	err := fn(u.tx)
	u.results = append(u.results, err)
	return err
}

// committed reports, for each transaction in order, whether it committed.
func (u *transactionUoW) committed() []bool {
	out := make([]bool, len(u.results))
	for i, err := range u.results {
		out[i] = err == nil
	}
	return out
}

type testTransactions struct {
	svc          *Transactions
	log          *callLog
	portfolios   *fakePortfolios
	transactions *fakeTransactions
	instruments  *fakeInstruments
	keys         *fakeTransactionKeys
	provider     *fakeProvider
	limits       *fakeRateLimits
	scopes       *[]domain.RateLimitScope
	audit        *fakeAudit
	uow          *transactionUoW
	clock        *fakeClock
	logs         *bytes.Buffer
	caller       domain.Principal
	meta         domain.RequestMeta
	portfolio    domain.Portfolio // the caller's own portfolio
	aapl         domain.Instrument
}

func newTestTransactions(t *testing.T) testTransactions {
	t.Helper()
	log := &callLog{}
	clock := newFakeClock()
	portfolios := &fakePortfolios{log: log, stored: map[domain.ID]domain.Portfolio{}}
	aapl := mustInstrument(t, "AAPL", clock.Now())
	instruments := &fakeInstruments{log: log, bySymbol: map[string]domain.Instrument{"AAPL": aapl}}
	transactions := &fakeTransactions{log: log, portfolios: portfolios}
	keys := &fakeTransactionKeys{log: log}
	audit := &fakeAudit{}
	uow := &transactionUoW{tx: transactionTx{
		portfolios: portfolios, transactions: transactions, keys: keys, instruments: instruments,
		audit: loggingAudit{log: log, audit: audit},
	}}
	provider := &fakeProvider{log: log, uow: uow, answer: domain.Instrument{
		Name: "Microsoft Corporation", Exchange: "NASDAQ", AssetClass: domain.AssetClassUSEquity,
		Status: domain.InstrumentStatusActive, Source: domain.InstrumentSourceDemo,
	}}
	limits := &fakeRateLimits{decision: domain.RateLimitDecision{Allowed: true}}
	scopes := &[]domain.RateLimitScope{}
	logs := &bytes.Buffer{}
	svc, err := NewTransactions(TransactionDeps{
		Store: transactionStore{
			portfolios: portfolios, transactions: transactions, instruments: instruments,
			limits: loggingLimits{log: log, limits: limits, scopes: scopes},
		},
		UnitOfWork: uow, Clock: clock, Limits: newTestPolicy(t), Instruments: provider,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("NewTransactions() error = %v", err)
	}
	tt := testTransactions{
		svc: svc, log: log, portfolios: portfolios, transactions: transactions, instruments: instruments, keys: keys,
		provider: provider, limits: limits, scopes: scopes, audit: audit, uow: uow, clock: clock, logs: logs,
		caller: domain.Principal{UserID: mustID(t), SessionID: mustID(t)}, meta: testMeta(t), aapl: aapl,
	}
	tt.portfolio = tt.seedPortfolio(t, tt.caller.UserID)
	return tt
}

func (tt testTransactions) seedPortfolio(t *testing.T, owner domain.ID) domain.Portfolio {
	t.Helper()
	p := domain.Portfolio{
		ID: mustID(t), UserID: owner, Name: mustPortfolioInput(t, "Long term", "").Name, Currency: domain.CurrencyUSD,
		Version: mustVersion(t, 1), CreatedAt: tt.clock.Now(), UpdatedAt: tt.clock.Now(),
	}
	tt.portfolios.stored[p.ID] = p
	return p
}

// seedTrade stores an AAPL trade the portfolio already has, as History would return it.
func (tt testTransactions) seedTrade(t *testing.T, in domain.TransactionInput) domain.Transaction {
	t.Helper()
	tt.transactions.nextSeq++
	tr := domain.Transaction{
		ID: mustID(t), PortfolioID: tt.portfolio.ID, InstrumentID: tt.aapl.ID, Symbol: tt.aapl.Symbol,
		Sequence: tt.transactions.nextSeq, Side: in.Side, Quantity: in.Quantity, Price: in.Price, Fee: in.Fee,
		ExecutedAt: in.ExecutedAt, Version: mustVersion(t, 1), CreatedAt: tt.clock.Now(), UpdatedAt: tt.clock.Now(),
	}
	tt.transactions.rows = append(tt.transactions.rows, tr)
	return tr
}

// requireNothingCommitted fails unless exactly one transaction ran and it rolled back.
func (tt testTransactions) requireNothingCommitted(t *testing.T) {
	t.Helper()
	if got := tt.uow.committed(); !slices.Equal(got, []bool{false}) {
		t.Errorf("transactions committed = %v, want one that rolled back", got)
	}
}

func (tt testTransactions) create(t *testing.T, in domain.TransactionInput) (domain.Transaction, error) {
	t.Helper()
	return tt.svc.Create(t.Context(), tt.caller, tt.portfolio.ID, "key-1", in, tt.meta)
}

func mustInstrument(t *testing.T, symbol string, at time.Time) domain.Instrument {
	t.Helper()
	i, err := domain.NewInstrument(domain.Instrument{
		ID: mustID(t), Symbol: mustSymbol(t, symbol), Name: symbol + " Inc.", Exchange: "NASDAQ",
		AssetClass: domain.AssetClassUSEquity, Status: domain.InstrumentStatusActive, Source: domain.InstrumentSourceDemo,
		VerifiedAt: at, CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func mustSymbol(t *testing.T, s string) domain.Symbol {
	t.Helper()
	sym, err := domain.ParseSymbol(s)
	if err != nil {
		t.Fatal(err)
	}
	return sym
}

func tradeInput(t *testing.T, symbol, side, quantity, executedAt string) domain.TransactionInput {
	t.Helper()
	in, err := domain.ParseTransactionInput(symbol, side, quantity, "200", "1", executedAt, newFakeClock().Now())
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func snapshotOf(t *testing.T, tr domain.Transaction) string {
	t.Helper()
	raw, err := domain.MarshalTransactionSnapshot(tr)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (tt testTransactions) eventsOf(eventType domain.EventType) []domain.AuditEvent {
	var out []domain.AuditEvent
	for _, e := range tt.audit.events {
		if e.EventType == eventType {
			out = append(out, e)
		}
	}
	return out
}

func TestCreateTradeWritesInOneTransaction(t *testing.T) {
	tt := newTestTransactions(t)
	in := tradeInput(t, "AAPL", "buy", "0.5", "2026-10-01T14:30:00Z")

	got, err := tt.create(t, in)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	want := []string{
		"Instruments.BySymbol", // the known symbol is read before the transaction
		"Portfolios.LockOwned", "Idempotency.Get", "Transactions.Insert", "Transactions.History",
		"Audit.Append", "Idempotency.Save",
	}
	if !slices.Equal(tt.log.calls, want) {
		t.Errorf("calls = %v, want %v", tt.log.calls, want)
	}
	if !slices.Equal(tt.uow.committed(), []bool{true}) || len(tt.provider.runsAtCall) != 0 {
		t.Errorf("transactions committed = %v, provider calls = %d; want one commit and no call", tt.uow.committed(), len(tt.provider.runsAtCall))
	}
	if got.Sequence != 1 || got.PortfolioID != tt.portfolio.ID || got.InstrumentID != tt.aapl.ID || got.Symbol != tt.aapl.Symbol ||
		got.Version.Int32() != 1 || got.Side != domain.SideBuy || got.Quantity.String() != "0.5" ||
		!got.ExecutedAt.Equal(in.ExecutedAt) || !got.CreatedAt.Equal(tt.clock.Now()) || !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("Create() = %+v", got)
	}

	if len(tt.keys.saved) != 1 {
		t.Fatalf("saved records = %d, want 1", len(tt.keys.saved))
	}
	rec := tt.keys.saved[0]
	wantScope := tt.createScope()
	if tt.keys.gotScope != wantScope || tt.keys.gotKey != "key-1" {
		t.Errorf("key looked up as %+v %q, want %+v key-1", tt.keys.gotScope, tt.keys.gotKey, wantScope)
	}
	if rec.Scope != wantScope || rec.Key != "key-1" || !bytes.Equal(rec.RequestHash, in.RequestHash()) {
		t.Errorf("record = %+v, want scope %+v, key-1 and the request hash", rec, wantScope)
	}
	if !rec.CreatedAt.Equal(tt.clock.Now()) || !rec.ExpiresAt.Equal(rec.CreatedAt.Add(24*time.Hour)) {
		t.Errorf("record created %v expires %v, want now and 24h later", rec.CreatedAt, rec.ExpiresAt)
	}
	replayed, err := domain.ParseTransactionSnapshot(rec.Result)
	if err != nil {
		t.Fatalf("stored result does not parse: %v", err)
	}
	if snapshotOf(t, replayed) != snapshotOf(t, got) {
		t.Errorf("stored result = %s, want the returned transaction %s", snapshotOf(t, replayed), snapshotOf(t, got))
	}

	if len(tt.audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(tt.audit.events))
	}
	e := tt.audit.events[0]
	if e.EventType != domain.EventTransactionCreated || e.Outcome != domain.AuditOutcomeSuccess ||
		e.ActorUserID != tt.caller.UserID || e.SessionID != tt.caller.SessionID || e.RequestID != tt.meta.RequestID ||
		e.TargetType != "transaction" || e.TargetID != got.ID.String() {
		t.Errorf("audit event = %+v", e)
	}
	m := auditMetadataOf(t, e)
	after, _ := m["after"].(map[string]any)
	wantAfter := map[string]any{
		"symbol": "AAPL", "side": "buy", "quantity": "0.5", "price": "200", "fee": "1", "executed_at": "2026-10-01T14:30:00Z",
	}
	if len(after) != len(wantAfter) {
		t.Errorf("after = %v, want %v", after, wantAfter)
	}
	for k, v := range wantAfter {
		if after[k] != v {
			t.Errorf("after[%s] = %v, want %v", k, after[k], v)
		}
	}
	if m["portfolio_id"] != tt.portfolio.ID.String() || m["ip"] != tt.meta.MaskedIP || m["user_agent"] != tt.meta.UserAgent {
		t.Errorf("metadata = %v, want portfolio_id, ip and user_agent", m)
	}
	if _, has := m["before"]; has {
		t.Errorf("metadata = %v, a created trade has no before", m)
	}
}

// The idempotency result is what the database stored (its sequence and timestamps), not the row
// the service built before the insert; a result that would not parse back fails the whole request
// instead of committing a key whose every retry would answer 500.
func TestCreateStoresTheRowTheDatabaseReturned(t *testing.T) {
	t.Run("the database row is stored", func(t *testing.T) {
		tt := newTestTransactions(t)
		dbTime := tt.clock.Now().Add(3 * time.Second)
		tt.transactions.stored = func(tr *domain.Transaction) { tr.CreatedAt, tr.UpdatedAt = dbTime, dbTime }

		got, err := tt.create(t, tradeInput(t, "AAPL", "buy", "1", "2026-10-01T14:30:00Z"))
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		replayed, err := domain.ParseTransactionSnapshot(tt.keys.saved[0].Result)
		if err != nil {
			t.Fatal(err)
		}
		if !got.CreatedAt.Equal(dbTime) || !replayed.CreatedAt.Equal(dbTime) || replayed.Sequence != got.Sequence {
			t.Errorf("returned %+v, stored %+v; want the database row in both", got, replayed)
		}
	})
	t.Run("a row that cannot be replayed fails the request", func(t *testing.T) {
		tt := newTestTransactions(t)
		tt.transactions.stored = func(tr *domain.Transaction) { tr.Symbol = domain.Symbol{} }

		_, err := tt.create(t, tradeInput(t, "AAPL", "buy", "1", "2026-10-01T14:30:00Z"))
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != domain.CodeInternal {
			t.Fatalf("Create() error = %v, want internal", err)
		}
		if slices.Contains(tt.log.calls, "Idempotency.Save") || slices.Contains(tt.log.calls, "Audit.Append") {
			t.Errorf("calls = %v, want neither audit nor key", tt.log.calls)
		}
		tt.requireNothingCommitted(t)
	})
}

func TestCreateLooksUpANewSymbolBeforeTheTransaction(t *testing.T) {
	tt := newTestTransactions(t)
	winner := mustInstrument(t, "MSFT", tt.clock.Now())
	tt.instruments.raceWinner = &winner // a concurrent request stored MSFT first

	got, err := tt.create(t, tradeInput(t, "MSFT", "buy", "2", "2026-10-01T14:30:00Z"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	wantStart := []string{"Instruments.BySymbol", "Portfolios.GetOwned", "RateLimits.Admit", "Provider.Lookup", "Portfolios.LockOwned"}
	if len(tt.log.calls) < len(wantStart) || !slices.Equal(tt.log.calls[:len(wantStart)], wantStart) {
		t.Errorf("calls = %v, want them to start with %v", tt.log.calls, wantStart)
	}
	if !slices.Equal(tt.provider.runsAtCall, []int{0}) {
		t.Errorf("provider called with %v transactions started, want once before any", tt.provider.runsAtCall)
	}
	upsert, insert := slices.Index(tt.log.calls, "Instruments.Upsert"), slices.Index(tt.log.calls, "Transactions.Insert")
	if upsert < 0 || insert < 0 || upsert > insert || upsert < slices.Index(tt.log.calls, "Portfolios.LockOwned") {
		t.Errorf("calls = %v, want Instruments.Upsert under the lock before Transactions.Insert", tt.log.calls)
	}
	if !slices.Equal(*tt.scopes, []domain.RateLimitScope{domain.RateLimitInstrumentLookupUser}) {
		t.Errorf("limits asked = %v, want instrument_lookup_user", *tt.scopes)
	}
	wantRules, err := newTestPolicy(t).InstrumentLookup(tt.caller.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tt.limits.rules) != 1 || !bytes.Equal(tt.limits.rules[0].KeyHash, wantRules[0].KeyHash) {
		t.Error("the lookup limit must be keyed by the caller")
	}
	up := tt.instruments.upserted[0]
	if up.ID.IsZero() || up.Symbol.String() != "MSFT" || !up.VerifiedAt.Equal(tt.clock.Now()) ||
		!up.CreatedAt.Equal(tt.clock.Now()) || !up.UpdatedAt.Equal(tt.clock.Now()) || up.Name != "Microsoft Corporation" {
		t.Errorf("upserted = %+v, want the provider answer with an ID and the times set", up)
	}
	if got.InstrumentID != winner.ID || tt.transactions.inserted[0].InstrumentID != winner.ID {
		t.Errorf("instrument ID = %v, want the stored row's %v", got.InstrumentID, winner.ID)
	}
}

func TestCreateOnAForeignPortfolioNeverReachesTheProvider(t *testing.T) {
	tt := newTestTransactions(t)
	theirs := tt.seedPortfolio(t, mustID(t))
	tt.portfolios.existsAnswe = true

	_, err := tt.svc.Create(t.Context(), tt.caller, theirs.ID, "key-1", tradeInput(t, "MSFT", "buy", "1", "2026-10-01T14:30:00Z"), tt.meta)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create() error = %v, want not_found", err)
	}
	if slices.Contains(tt.log.calls, "Provider.Lookup") || slices.Contains(*tt.scopes, domain.RateLimitInstrumentLookupUser) {
		t.Errorf("calls = %v, limits = %v: a foreign portfolio must not spend the lookup quota", tt.log.calls, *tt.scopes)
	}
	if slices.Contains(tt.log.calls, "Portfolios.LockOwned") {
		t.Errorf("calls = %v, want no write transaction", tt.log.calls)
	}
	denied := tt.eventsOf(domain.EventSecurityAccessDenied)
	if len(denied) != 1 || len(tt.audit.events) != 1 {
		t.Fatalf("audit events = %+v, want one security.access_denied", tt.audit.events)
	}
	if e := denied[0]; e.TargetType != "portfolio" || e.TargetID != theirs.ID.String() || e.ActorUserID != tt.caller.UserID {
		t.Errorf("audit event = %+v", e)
	}
}

func TestCreateOnAForeignPortfolioWithAKnownSymbolIsDeniedOnce(t *testing.T) {
	tt := newTestTransactions(t)
	theirs := tt.seedPortfolio(t, mustID(t))
	tt.portfolios.existsAnswe = true

	_, err := tt.svc.Create(t.Context(), tt.caller, theirs.ID, "key-1", tradeInput(t, "AAPL", "buy", "1", "2026-10-01T14:30:00Z"), tt.meta)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create() error = %v, want not_found", err)
	}
	if len(tt.eventsOf(domain.EventSecurityAccessDenied)) != 1 || len(tt.audit.events) != 1 {
		t.Errorf("audit events = %+v, want one security.access_denied", tt.audit.events)
	}
	if slices.Contains(tt.log.calls, "Idempotency.Get") || slices.Contains(tt.log.calls, "Transactions.Insert") {
		t.Errorf("calls = %v, want nothing after the lock", tt.log.calls)
	}
}

func TestInPortfolioRecordsADenialOnlyForTheLock(t *testing.T) {
	tt := newTestTransactions(t)
	tt.portfolios.existsAnswe = true

	err := tt.svc.inPortfolio(t.Context(), tt.caller, tt.portfolio.ID, tt.meta, func(port.TxRepositories) error {
		return domain.ErrNotFound // a row of the caller's own portfolio is missing
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("inPortfolio() error = %v, want not_found", err)
	}
	if slices.Contains(tt.log.calls, "Portfolios.Exists") || len(tt.audit.events) != 0 {
		t.Errorf("calls = %v, events = %d: a missing row in your own portfolio is not an access denial", tt.log.calls, len(tt.audit.events))
	}
}

func TestCreateSymbolProblems(t *testing.T) {
	newSymbol := func(t *testing.T) domain.TransactionInput {
		return tradeInput(t, "MSFT", "buy", "1", "2026-10-01T14:30:00Z")
	}
	t.Run("the provider does not know the symbol", func(t *testing.T) {
		tt := newTestTransactions(t)
		tt.provider.err = domain.ErrUnknownSymbol

		if _, err := tt.create(t, newSymbol(t)); !errors.Is(err, domain.ErrUnknownSymbol) {
			t.Fatalf("Create() error = %v, want unknown_symbol", err)
		}
		if tt.uow.runs != 0 {
			t.Errorf("transactions = %d, want none", tt.uow.runs)
		}
	})
	t.Run("the provider answers with another asset class", func(t *testing.T) {
		tt := newTestTransactions(t)
		tt.provider.answer.AssetClass = "crypto"

		if _, err := tt.create(t, newSymbol(t)); !errors.Is(err, domain.ErrUnknownSymbol) {
			t.Fatalf("Create() error = %v, want unknown_symbol", err)
		}
		if tt.uow.runs != 0 {
			t.Errorf("transactions = %d, want none", tt.uow.runs)
		}
	})
	t.Run("the provider answers for another symbol", func(t *testing.T) {
		tt := newTestTransactions(t)
		renamed := mustSymbol(t, "MSFTX") // phase 1 does not follow symbol changes
		tt.provider.renamedTo = &renamed

		if _, err := tt.create(t, newSymbol(t)); !errors.Is(err, domain.ErrUnknownSymbol) {
			t.Fatalf("Create() error = %v, want unknown_symbol", err)
		}
		if tt.uow.runs != 0 || len(tt.instruments.upserted) != 0 {
			t.Errorf("transactions = %d, upserts = %d; want nothing written", tt.uow.runs, len(tt.instruments.upserted))
		}
	})
	t.Run("the provider is down", func(t *testing.T) {
		tt := newTestTransactions(t)
		cause := errors.New("connection reset")
		tt.provider.err = cause

		_, err := tt.create(t, newSymbol(t))
		if !errors.Is(err, domain.ErrDependencyUnavailable) || !errors.Is(err, cause) {
			t.Fatalf("Create() error = %v, want dependency_unavailable keeping the cause", err)
		}
		if tt.uow.runs != 0 || len(tt.audit.events) != 0 || len(tt.instruments.upserted) != 0 {
			t.Errorf("transactions = %d, events = %d, upserts = %d; want nothing written", tt.uow.runs, len(tt.audit.events), len(tt.instruments.upserted))
		}
	})
	t.Run("an inactive instrument is still recorded", func(t *testing.T) {
		tt := newTestTransactions(t)
		tt.provider.answer.Status = domain.InstrumentStatusInactive

		if _, err := tt.create(t, newSymbol(t)); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if len(tt.instruments.upserted) != 1 || tt.instruments.upserted[0].Status != domain.InstrumentStatusInactive || len(tt.keys.saved) != 1 {
			t.Errorf("upserted = %+v, saved = %d", tt.instruments.upserted, len(tt.keys.saved))
		}
	})
	for _, marker := range []bool{true, false} {
		name := "the lookup limit refuses, between markers"
		if marker {
			name = "the lookup limit refuses, first of the minute"
		}
		t.Run(name, func(t *testing.T) {
			tt := newTestTransactions(t)
			tt.limits.decision = domain.RateLimitDecision{
				RetryAfter: 42 * time.Second, RecordDenialAudit: marker, Scope: domain.RateLimitInstrumentLookupUser,
			}

			_, err := tt.create(t, newSymbol(t))
			var de *domain.Error
			if !errors.As(err, &de) || de.Code != domain.CodeRateLimited || de.RetryAfter != 42*time.Second {
				t.Fatalf("Create() error = %v, want rate_limited with a 42s wait", err)
			}
			if slices.Contains(tt.log.calls, "Provider.Lookup") || slices.Contains(tt.log.calls, "Portfolios.LockOwned") {
				t.Errorf("calls = %v, want neither the provider nor the lock", tt.log.calls)
			}
			limited := tt.eventsOf(domain.EventSecurityRateLimited)
			if marker {
				if len(limited) != 1 || len(tt.audit.events) != 1 {
					t.Fatalf("audit events = %+v, want one security.rate_limited", tt.audit.events)
				}
				e := limited[0]
				if e.ActorUserID != tt.caller.UserID || e.SessionID != tt.caller.SessionID || e.TargetID != string(domain.RateLimitInstrumentLookupUser) {
					t.Errorf("audit event = %+v", e)
				}
				if m := auditMetadataOf(t, e); m["scope"] != string(domain.RateLimitInstrumentLookupUser) {
					t.Errorf("metadata = %v, want the scope", m)
				}
			} else if len(tt.audit.events) != 0 {
				t.Errorf("audit events = %+v, want none between markers", tt.audit.events)
			}
		})
	}
}

// createScope is the idempotency scope of a create in the caller's own portfolio.
func (tt testTransactions) createScope() domain.IdempotencyScope {
	return domain.IdempotencyScope{UserID: tt.caller.UserID, PortfolioID: tt.portfolio.ID, Operation: domain.IdempotencyOperationCreateTransaction}
}

func (tt testTransactions) storedRecord(t *testing.T, in domain.TransactionInput, createdAt time.Time, result domain.Transaction) *domain.IdempotencyRecord {
	t.Helper()
	raw, err := domain.MarshalTransactionSnapshot(result)
	if err != nil {
		t.Fatal(err)
	}
	return &domain.IdempotencyRecord{
		Scope:       tt.createScope(),
		Key:         "key-1",
		RequestHash: in.RequestHash(),
		Result:      json.RawMessage(raw),
		CreatedAt:   createdAt,
		ExpiresAt:   createdAt.Add(24 * time.Hour),
	}
}

func (tt testTransactions) firstResult(t *testing.T) domain.Transaction {
	t.Helper()
	in := tradeInput(t, "AAPL", "buy", "0.5", "2026-10-01T14:30:00Z")
	return domain.Transaction{
		ID: mustID(t), PortfolioID: tt.portfolio.ID, InstrumentID: tt.aapl.ID, Symbol: tt.aapl.Symbol, Sequence: 7,
		Side: in.Side, Quantity: in.Quantity, Price: in.Price, Fee: in.Fee, ExecutedAt: in.ExecutedAt, Version: mustVersion(t, 2),
		CreatedAt: tt.clock.Now().Add(-time.Hour), UpdatedAt: tt.clock.Now().Add(-time.Minute),
	}
}

func TestCreateReplaysTheSameKey(t *testing.T) {
	tt := newTestTransactions(t)
	in := tradeInput(t, "AAPL", "buy", "0.5", "2026-10-01T14:30:00Z")
	first := tt.firstResult(t)
	tt.keys.record = tt.storedRecord(t, in, tt.clock.Now().Add(-time.Hour), first)

	got, err := tt.create(t, in)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if snapshotOf(t, got) != snapshotOf(t, first) {
		t.Errorf("Create() = %s, want the stored %s", snapshotOf(t, got), snapshotOf(t, first))
	}
	if want := []string{"Instruments.BySymbol", "Portfolios.LockOwned", "Idempotency.Get"}; !slices.Equal(tt.log.calls, want) {
		t.Errorf("calls = %v, want %v", tt.log.calls, want)
	}
	if tt.keys.gotKey != "key-1" || tt.keys.gotScope != tt.createScope() || len(tt.audit.events) != 0 || len(tt.provider.runsAtCall) != 0 {
		t.Errorf("key = %+v %q, events = %d, provider calls = %d", tt.keys.gotScope, tt.keys.gotKey, len(tt.audit.events), len(tt.provider.runsAtCall))
	}
}

func TestCreateRefusesAKeyUsedForAnotherRequest(t *testing.T) {
	tt := newTestTransactions(t)
	earlier := tradeInput(t, "AAPL", "buy", "0.5", "2026-10-01T14:30:00Z")
	tt.keys.record = tt.storedRecord(t, earlier, tt.clock.Now().Add(-time.Hour), tt.firstResult(t))

	_, err := tt.create(t, tradeInput(t, "AAPL", "buy", "0.6", "2026-10-01T14:30:00Z"))
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("Create() error = %v, want idempotency_conflict", err)
	}
	if want := []string{"Instruments.BySymbol", "Portfolios.LockOwned", "Idempotency.Get"}; !slices.Equal(tt.log.calls, want) {
		t.Errorf("calls = %v, want %v", tt.log.calls, want)
	}
	if len(tt.audit.events) != 0 {
		t.Errorf("audit events = %d, want none", len(tt.audit.events))
	}
	tt.requireNothingCommitted(t)
}

func TestCreateReusesAnExpiredKey(t *testing.T) {
	tt := newTestTransactions(t)
	in := tradeInput(t, "AAPL", "buy", "0.5", "2026-10-01T14:30:00Z")
	// Exactly 24 hours old: expires_at <= now is expired, as the DeleteExpired query says.
	tt.keys.record = tt.storedRecord(t, tradeInput(t, "AAPL", "buy", "3", "2026-10-01T14:30:00Z"), tt.clock.Now().Add(-24*time.Hour), tt.firstResult(t))

	got, err := tt.create(t, in)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	want := []string{
		"Instruments.BySymbol", "Portfolios.LockOwned", "Idempotency.Get", "Idempotency.DeleteExpired",
		"Transactions.Insert", "Transactions.History", "Audit.Append", "Idempotency.Save",
	}
	if !slices.Equal(tt.log.calls, want) {
		t.Errorf("calls = %v, want %v", tt.log.calls, want)
	}
	if !slices.Equal(tt.keys.deleted, []time.Time{tt.clock.Now()}) {
		t.Errorf("DeleteExpired at %v, want now", tt.keys.deleted)
	}
	if tt.keys.deletedScope != tt.createScope() || tt.keys.deletedKey != "key-1" {
		t.Errorf("DeleteExpired(%+v, %q), want %+v key-1", tt.keys.deletedScope, tt.keys.deletedKey, tt.createScope())
	}
	rec := tt.keys.saved[0]
	if !bytes.Equal(rec.RequestHash, in.RequestHash()) || !rec.CreatedAt.Equal(tt.clock.Now()) {
		t.Errorf("saved record = %+v, want the new request from now", rec)
	}
	if replayed, err := domain.ParseTransactionSnapshot(rec.Result); err != nil || replayed.ID != got.ID {
		t.Errorf("saved result = %+v (%v), want the new transaction", replayed, err)
	}
}

func TestCreateRefusesASellPastTheHolding(t *testing.T) {
	tt := newTestTransactions(t)
	tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "1", "2026-09-01T14:30:00Z"))
	// A later buy does not help a sale that comes before it in the history.
	tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "5", "2026-10-05T14:30:00Z"))

	_, err := tt.create(t, tradeInput(t, "AAPL", "sell", "2", "2026-10-01T14:30:00Z"))
	if !errors.Is(err, domain.ErrInsufficientShares) {
		t.Fatalf("Create() error = %v, want insufficient_shares", err)
	}
	if slices.Contains(tt.log.calls, "Audit.Append") || slices.Contains(tt.log.calls, "Idempotency.Save") {
		t.Errorf("calls = %v, want neither audit nor key", tt.log.calls)
	}
	tt.requireNothingCommitted(t)
}

func TestCreateFailsWhenTheAuditCannotBeWritten(t *testing.T) {
	tt := newTestTransactions(t)
	tt.audit.err = errors.New("disk full")

	_, err := tt.create(t, tradeInput(t, "AAPL", "buy", "1", "2026-10-01T14:30:00Z"))
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != domain.CodeAuditWriteFailed {
		t.Fatalf("Create() error = %v, want audit_write_failed", err)
	}
	if slices.Contains(tt.log.calls, "Idempotency.Save") {
		t.Errorf("calls = %v, want no key saved", tt.log.calls)
	}
	tt.requireNothingCommitted(t)
}

func TestCreateNeedsACaller(t *testing.T) {
	tt := newTestTransactions(t)

	_, err := tt.svc.Create(t.Context(), domain.Principal{}, tt.portfolio.ID, "key-1", tradeInput(t, "MSFT", "buy", "1", "2026-10-01T14:30:00Z"), tt.meta)
	if !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("Create() error = %v, want unauthenticated", err)
	}
	if len(tt.log.calls) != 0 || tt.uow.runs != 0 {
		t.Errorf("repositories touched: %v (%d transactions)", tt.log.calls, tt.uow.runs)
	}
}

func TestNewTransactionsNeedsEveryDependency(t *testing.T) {
	if _, err := NewTransactions(TransactionDeps{}); err == nil {
		t.Error("NewTransactions(no deps) error = nil, want an error")
	}
}

// A malformed Idempotency-Key is refused before anything is read, so it cannot spend the
// instrument lookup quota on a request that was never going to be recorded.
func TestCreateRefusesABadIdempotencyKeyFirst(t *testing.T) {
	for name, key := range map[string]string{
		"empty":          "",
		"with a space":   "key 1",
		"129 bytes":      string(bytes.Repeat([]byte("k"), 129)),
		"not ASCII":      "clé",
		"a control byte": "key\n",
	} {
		t.Run(name, func(t *testing.T) {
			tt := newTestTransactions(t)

			_, err := tt.svc.Create(t.Context(), tt.caller, tt.portfolio.ID, key, tradeInput(t, "MSFT", "buy", "1", "2026-10-01T14:30:00Z"), tt.meta)
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("Create() error = %v, want invalid_input", err)
			}
			if len(tt.log.calls) != 0 || tt.uow.runs != 0 || len(*tt.scopes) != 0 {
				t.Errorf("calls = %v, transactions = %d, limits = %v; want nothing touched", tt.log.calls, tt.uow.runs, *tt.scopes)
			}
		})
	}
}

// The bookkeeping of a refused write logs its failure under a stable event name that alerts and
// log queries can match; the answer stays not_found.
func TestDenialBookkeepingFailureIsLoggedByName(t *testing.T) {
	cases := map[string]func(tt testTransactions){
		"the existence lookup fails": func(tt testTransactions) { tt.portfolios.existsErr = errors.New("connection reset") },
		"the limiter is down":        func(tt testTransactions) { tt.limits.err = errors.New("connection refused") },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			tt := newTestTransactions(t)
			theirs := tt.seedPortfolio(t, mustID(t))
			tt.portfolios.existsAnswe = true
			breakIt(tt)

			err := tt.svc.Delete(t.Context(), tt.caller, theirs.ID, mustID(t), mustVersion(t, 1), tt.meta)
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("Delete() error = %v, want not_found", err)
			}
			if !bytes.Contains(tt.logs.Bytes(), []byte(`"event":"access_denial.bookkeeping_failed"`)) ||
				!bytes.Contains(tt.logs.Bytes(), []byte(tt.meta.RequestID.String())) {
				t.Errorf("log %q, want event access_denial.bookkeeping_failed with the request ID", tt.logs.String())
			}
		})
	}
}

func tradeOf(t *testing.T, symbol, side, quantity, price, fee, executedAt string) domain.TransactionInput {
	t.Helper()
	in, err := domain.ParseTransactionInput(symbol, side, quantity, price, fee, executedAt, newFakeClock().Now())
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func (tt testTransactions) update(t *testing.T, id domain.ID, expected int64, in domain.TransactionInput) (domain.Transaction, error) {
	t.Helper()
	return tt.svc.Update(t.Context(), tt.caller, tt.portfolio.ID, id, mustVersion(t, expected), in, tt.meta)
}

func (tt testTransactions) delete(t *testing.T, id domain.ID, expected int64) error {
	t.Helper()
	return tt.svc.Delete(t.Context(), tt.caller, tt.portfolio.ID, id, mustVersion(t, expected), tt.meta)
}

// requireTradeFields checks an audited before or after: exactly the six trade fields, as strings.
func requireTradeFields(t *testing.T, name string, got any, want map[string]any) {
	t.Helper()
	m, ok := got.(map[string]any)
	if !ok || len(m) != len(want) {
		t.Errorf("%s = %v, want exactly %v", name, got, want)
		return
	}
	for k, v := range want {
		if s, isString := m[k].(string); !isString || s != v {
			t.Errorf("%s[%s] = %#v, want the string %v", name, k, m[k], v)
		}
	}
}

func TestUpdateChecksTheVersionUnderTheLock(t *testing.T) {
	t.Run("update", func(t *testing.T) {
		tt := newTestTransactions(t)
		seeded := tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "1", "2026-09-01T14:30:00Z"))
		tt.transactions.rows[0].Version = mustVersion(t, 2) // someone else changed it after the client read it

		_, err := tt.update(t, seeded.ID, 1, tradeInput(t, "AAPL", "buy", "3", "2026-09-01T14:30:00Z"))
		if !errors.Is(err, domain.ErrVersionConflict) {
			t.Fatalf("Update() error = %v, want version_conflict", err)
		}
		if want := []string{"Instruments.BySymbol", "Portfolios.LockOwned", "Transactions.Get"}; !slices.Equal(tt.log.calls, want) {
			t.Errorf("calls = %v, want %v", tt.log.calls, want)
		}
		if len(tt.audit.events) != 0 {
			t.Errorf("audit events = %+v, want none", tt.audit.events)
		}
		tt.requireNothingCommitted(t)
	})
	t.Run("delete", func(t *testing.T) {
		tt := newTestTransactions(t)
		seeded := tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "1", "2026-09-01T14:30:00Z"))
		tt.transactions.rows[0].Version = mustVersion(t, 2)

		if err := tt.delete(t, seeded.ID, 1); !errors.Is(err, domain.ErrVersionConflict) {
			t.Fatalf("Delete() error = %v, want version_conflict", err)
		}
		if want := []string{"Portfolios.LockOwned", "Transactions.Get"}; !slices.Equal(tt.log.calls, want) {
			t.Errorf("calls = %v, want %v", tt.log.calls, want)
		}
		if len(tt.audit.events) != 0 || len(tt.transactions.rows) != 1 {
			t.Errorf("audit events = %d, rows = %d; want none and the trade kept", len(tt.audit.events), len(tt.transactions.rows))
		}
		tt.requireNothingCommitted(t)
	})
}

func TestUpdateAuditsBeforeAndAfter(t *testing.T) {
	tt := newTestTransactions(t)
	seeded := tt.seedTrade(t, tradeOf(t, "AAPL", "buy", "1", "200", "1", "2026-09-01T14:30:00Z"))
	tt.clock.Advance(time.Hour)

	got, err := tt.update(t, seeded.ID, 1, tradeOf(t, "AAPL", "buy", "2.50", "180.25", "0", "2026-09-02T15:00:00+07:00"))
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	want := []string{
		"Instruments.BySymbol", "Portfolios.LockOwned", "Transactions.Get", "Transactions.Update",
		"Transactions.History", "Audit.Append",
	}
	if !slices.Equal(tt.log.calls, want) {
		t.Errorf("calls = %v, want %v", tt.log.calls, want)
	}
	if !slices.Equal(tt.uow.committed(), []bool{true}) {
		t.Errorf("transactions committed = %v, want one commit", tt.uow.committed())
	}

	passed := tt.transactions.updated[0]
	if passed.ID != seeded.ID || passed.PortfolioID != tt.portfolio.ID || passed.Version.Int32() != 1 ||
		passed.Sequence != seeded.Sequence || !passed.CreatedAt.Equal(seeded.CreatedAt) || !passed.UpdatedAt.Equal(tt.clock.Now()) {
		t.Errorf("Update got %+v, want the current version, the original sequence and creation time, and updated now", passed)
	}
	if got.Version.Int32() != 2 || got.Sequence != seeded.Sequence || got.Quantity.String() != "2.5" ||
		got.Price.String() != "180.25" || got.Fee.String() != "0" || !got.UpdatedAt.Equal(tt.clock.Now()) {
		t.Errorf("Update() = %+v, want the stored row at version 2", got)
	}

	if len(tt.audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(tt.audit.events))
	}
	e := tt.audit.events[0]
	if e.EventType != domain.EventTransactionUpdated || e.Outcome != domain.AuditOutcomeSuccess ||
		e.ActorUserID != tt.caller.UserID || e.SessionID != tt.caller.SessionID || e.RequestID != tt.meta.RequestID ||
		e.TargetType != "transaction" || e.TargetID != seeded.ID.String() {
		t.Errorf("audit event = %+v", e)
	}
	m := auditMetadataOf(t, e)
	for k := range m {
		if !slices.Contains([]string{"portfolio_id", "ip", "user_agent", "before", "after"}, k) {
			t.Errorf("metadata has %q; only portfolio_id, ip, user_agent, before and after are allowed", k)
		}
	}
	if m["portfolio_id"] != tt.portfolio.ID.String() || m["ip"] != tt.meta.MaskedIP || m["user_agent"] != tt.meta.UserAgent {
		t.Errorf("metadata = %v, want portfolio_id, ip and user_agent", m)
	}
	requireTradeFields(t, "before", m["before"], map[string]any{
		"symbol": "AAPL", "side": "buy", "quantity": "1", "price": "200", "fee": "1", "executed_at": "2026-09-01T14:30:00Z",
	})
	requireTradeFields(t, "after", m["after"], map[string]any{
		"symbol": "AAPL", "side": "buy", "quantity": "2.5", "price": "180.25", "fee": "0", "executed_at": "2026-09-02T08:00:00Z",
	})
}

func TestUpdateToANewSymbolStoresTheInstrumentFirst(t *testing.T) {
	tt := newTestTransactions(t)
	seeded := tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "1", "2026-09-01T14:30:00Z"))
	winner := mustInstrument(t, "MSFT", tt.clock.Now())
	tt.instruments.raceWinner = &winner // a concurrent request stored MSFT first

	got, err := tt.update(t, seeded.ID, 1, tradeInput(t, "MSFT", "buy", "1", "2026-09-01T14:30:00Z"))
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	want := []string{
		"Instruments.BySymbol", "Portfolios.GetOwned", "RateLimits.Admit", "Provider.Lookup",
		"Portfolios.LockOwned", "Transactions.Get", "Instruments.Upsert", "Transactions.Update",
		"Transactions.History", "Audit.Append",
	}
	if !slices.Equal(tt.log.calls, want) {
		t.Errorf("calls = %v, want %v", tt.log.calls, want)
	}
	if !slices.Equal(tt.provider.runsAtCall, []int{0}) {
		t.Errorf("provider called with %v transactions started, want once before any", tt.provider.runsAtCall)
	}
	if tt.transactions.updated[0].InstrumentID != winner.ID || got.InstrumentID != winner.ID || got.Symbol.String() != "MSFT" {
		t.Errorf("Update got instrument %v, returned %v %s; want the stored row's %v MSFT",
			tt.transactions.updated[0].InstrumentID, got.InstrumentID, got.Symbol, winner.ID)
	}
	m := auditMetadataOf(t, tt.audit.events[0])
	before, _ := m["before"].(map[string]any)
	after, _ := m["after"].(map[string]any)
	if before["symbol"] != "AAPL" || after["symbol"] != "MSFT" {
		t.Errorf("before = %v, after = %v; want AAPL then MSFT", before, after)
	}
	if !slices.Equal(tt.uow.committed(), []bool{true}) {
		t.Errorf("transactions committed = %v, want one commit", tt.uow.committed())
	}
}

func TestUpdateAndDeleteRefuseAnImpossibleHistory(t *testing.T) {
	// The buy funds a later sale of everything it bought.
	seed := func(t *testing.T) (testTransactions, domain.Transaction) {
		tt := newTestTransactions(t)
		buy := tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "1", "2026-09-01T14:30:00Z"))
		tt.seedTrade(t, tradeInput(t, "AAPL", "sell", "1", "2026-10-01T14:30:00Z"))
		return tt, buy
	}
	changes := map[string]func(tt testTransactions, buy domain.Transaction) error{
		"buy fewer": func(tt testTransactions, buy domain.Transaction) error {
			_, err := tt.update(t, buy.ID, 1, tradeInput(t, "AAPL", "buy", "0.5", "2026-09-01T14:30:00Z"))
			return err
		},
		"buy after the sale": func(tt testTransactions, buy domain.Transaction) error {
			_, err := tt.update(t, buy.ID, 1, tradeInput(t, "AAPL", "buy", "1", "2026-10-02T14:30:00Z"))
			return err
		},
		"move the buy to another symbol": func(tt testTransactions, buy domain.Transaction) error {
			tt.instruments.bySymbol["MSFT"] = mustInstrument(t, "MSFT", tt.clock.Now())
			_, err := tt.update(t, buy.ID, 1, tradeInput(t, "MSFT", "buy", "1", "2026-09-01T14:30:00Z"))
			return err
		},
		"delete the buy": func(tt testTransactions, buy domain.Transaction) error {
			return tt.delete(t, buy.ID, 1)
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			tt, buy := seed(t)

			err := change(tt, buy)
			if !errors.Is(err, domain.ErrInvalidHistory) || errors.Is(err, domain.ErrInsufficientShares) {
				t.Fatalf("error = %v, want invalid_history and not insufficient_shares", err)
			}
			if !slices.Contains(tt.log.calls, "Transactions.History") || slices.Contains(tt.log.calls, "Audit.Append") {
				t.Errorf("calls = %v, want the history replayed and no audit", tt.log.calls)
			}
			tt.requireNothingCommitted(t)
		})
	}
}

func TestDeleteAuditsWhatWasRemoved(t *testing.T) {
	tt := newTestTransactions(t)
	seeded := tt.seedTrade(t, tradeOf(t, "AAPL", "buy", "0.5", "200", "1", "2026-10-01T14:30:00Z"))

	if err := tt.delete(t, seeded.ID, 1); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	want := []string{"Portfolios.LockOwned", "Transactions.Get", "Transactions.Delete", "Transactions.History", "Audit.Append"}
	if !slices.Equal(tt.log.calls, want) {
		t.Errorf("calls = %v, want %v", tt.log.calls, want)
	}
	if !slices.Equal(tt.transactions.deleted, []domain.ID{seeded.ID}) || len(tt.transactions.rows) != 0 {
		t.Errorf("deleted = %v, rows left = %d; want the trade removed", tt.transactions.deleted, len(tt.transactions.rows))
	}
	if !slices.Equal(tt.uow.committed(), []bool{true}) {
		t.Errorf("transactions committed = %v, want one commit", tt.uow.committed())
	}
	if len(tt.audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(tt.audit.events))
	}
	e := tt.audit.events[0]
	if e.EventType != domain.EventTransactionDeleted || e.Outcome != domain.AuditOutcomeSuccess ||
		e.ActorUserID != tt.caller.UserID || e.SessionID != tt.caller.SessionID ||
		e.TargetType != "transaction" || e.TargetID != seeded.ID.String() {
		t.Errorf("audit event = %+v", e)
	}
	m := auditMetadataOf(t, e)
	requireTradeFields(t, "before", m["before"], map[string]any{
		"symbol": "AAPL", "side": "buy", "quantity": "0.5", "price": "200", "fee": "1", "executed_at": "2026-10-01T14:30:00Z",
	})
	if _, has := m["after"]; has || m["portfolio_id"] != tt.portfolio.ID.String() {
		t.Errorf("metadata = %v, want the portfolio and no after for a deleted trade", m)
	}
}

func TestUpdateAndDeleteFailWhenTheAuditCannotBeWritten(t *testing.T) {
	for name, change := range map[string]func(tt testTransactions, id domain.ID) error{
		"update": func(tt testTransactions, id domain.ID) error {
			_, err := tt.update(t, id, 1, tradeInput(t, "AAPL", "buy", "2", "2026-09-01T14:30:00Z"))
			return err
		},
		"delete": func(tt testTransactions, id domain.ID) error { return tt.delete(t, id, 1) },
	} {
		t.Run(name, func(t *testing.T) {
			tt := newTestTransactions(t)
			seeded := tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "1", "2026-09-01T14:30:00Z"))
			tt.audit.err = errors.New("disk full")

			err := change(tt, seeded.ID)
			var de *domain.Error
			if !errors.As(err, &de) || de.Code != domain.CodeAuditWriteFailed {
				t.Fatalf("error = %v, want audit_write_failed", err)
			}
			tt.requireNothingCommitted(t)
		})
	}
}

func TestMissingTradeInOwnPortfolioIsNotAnAccessDenial(t *testing.T) {
	for name, change := range map[string]func(tt testTransactions, id domain.ID) error{
		"update": func(tt testTransactions, id domain.ID) error {
			_, err := tt.update(t, id, 1, tradeInput(t, "AAPL", "buy", "2", "2026-09-01T14:30:00Z"))
			return err
		},
		"delete": func(tt testTransactions, id domain.ID) error { return tt.delete(t, id, 1) },
	} {
		t.Run(name, func(t *testing.T) {
			tt := newTestTransactions(t)
			tt.portfolios.existsAnswe = true // the portfolio exists: it is the caller's own

			if err := change(tt, mustID(t)); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("error = %v, want not_found", err)
			}
			if slices.Contains(tt.log.calls, "Portfolios.Exists") || len(tt.audit.events) != 0 {
				t.Errorf("calls = %v, events = %+v: a missing trade in your own portfolio is not an access denial", tt.log.calls, tt.audit.events)
			}
			tt.requireNothingCommitted(t)
		})
	}
}

func TestUpdateOrDeleteOnAForeignPortfolioIsDenied(t *testing.T) {
	for name, change := range map[string]func(tt testTransactions, portfolioID domain.ID) error{
		"update": func(tt testTransactions, portfolioID domain.ID) error {
			_, err := tt.svc.Update(t.Context(), tt.caller, portfolioID, mustID(t), mustVersion(t, 1), tradeInput(t, "AAPL", "buy", "1", "2026-09-01T14:30:00Z"), tt.meta)
			return err
		},
		"update to a new symbol": func(tt testTransactions, portfolioID domain.ID) error {
			_, err := tt.svc.Update(t.Context(), tt.caller, portfolioID, mustID(t), mustVersion(t, 1), tradeInput(t, "MSFT", "buy", "1", "2026-09-01T14:30:00Z"), tt.meta)
			return err
		},
		"delete": func(tt testTransactions, portfolioID domain.ID) error {
			return tt.svc.Delete(t.Context(), tt.caller, portfolioID, mustID(t), mustVersion(t, 1), tt.meta)
		},
	} {
		t.Run(name, func(t *testing.T) {
			tt := newTestTransactions(t)
			theirs := tt.seedPortfolio(t, mustID(t))
			tt.portfolios.existsAnswe = true

			if err := change(tt, theirs.ID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("error = %v, want not_found", err)
			}
			denied := tt.eventsOf(domain.EventSecurityAccessDenied)
			if len(denied) != 1 || len(tt.audit.events) != 1 {
				t.Fatalf("audit events = %+v, want one security.access_denied", tt.audit.events)
			}
			if e := denied[0]; e.TargetType != "portfolio" || e.TargetID != theirs.ID.String() || e.ActorUserID != tt.caller.UserID {
				t.Errorf("audit event = %+v", e)
			}
			if slices.Contains(tt.log.calls, "Transactions.Get") || slices.Contains(tt.log.calls, "Provider.Lookup") {
				t.Errorf("calls = %v, want neither a trade read nor a provider call", tt.log.calls)
			}
			// Any write transaction rolled back; only the denial's own transaction commits.
			if got := tt.uow.committed(); len(got) == 0 || got[len(got)-1] != true || slices.Contains(got[:len(got)-1], true) {
				t.Errorf("transactions committed = %v, want only the denial committed", got)
			}
		})
	}
}

func TestUpdateAndDeleteNeedACaller(t *testing.T) {
	tt := newTestTransactions(t)
	seeded := tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "1", "2026-09-01T14:30:00Z"))

	_, err := tt.svc.Update(t.Context(), domain.Principal{}, tt.portfolio.ID, seeded.ID, mustVersion(t, 1), tradeInput(t, "MSFT", "buy", "1", "2026-09-01T14:30:00Z"), tt.meta)
	if !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("Update() error = %v, want unauthenticated", err)
	}
	if err := tt.svc.Delete(t.Context(), domain.Principal{}, tt.portfolio.ID, seeded.ID, mustVersion(t, 1), tt.meta); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("Delete() error = %v, want unauthenticated", err)
	}
	if len(tt.log.calls) != 0 || tt.uow.runs != 0 {
		t.Errorf("repositories touched: %v (%d transactions)", tt.log.calls, tt.uow.runs)
	}
}

func TestGetAndListNeedACallerAndWriteNoAudit(t *testing.T) {
	page, err := domain.ParsePage("", "")
	if err != nil {
		t.Fatal(err)
	}
	filter, err := domain.NewTransactionFilter(page, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("no caller", func(t *testing.T) {
		tt := newTestTransactions(t)
		seeded := tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "1", "2026-09-01T14:30:00Z"))

		if _, err := tt.svc.Get(t.Context(), domain.Principal{}, tt.portfolio.ID, seeded.ID); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Errorf("Get() error = %v, want unauthenticated", err)
		}
		if _, err := tt.svc.List(t.Context(), domain.Principal{}, tt.portfolio.ID, filter); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Errorf("List() error = %v, want unauthenticated", err)
		}
		if len(tt.log.calls) != 0 {
			t.Errorf("repositories touched: %v", tt.log.calls)
		}
	})
	t.Run("the caller's portfolio", func(t *testing.T) {
		tt := newTestTransactions(t)
		first := tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "1", "2026-09-01T14:30:00Z"))
		second := tt.seedTrade(t, tradeInput(t, "AAPL", "buy", "2", "2026-09-02T14:30:00Z"))

		got, err := tt.svc.Get(t.Context(), tt.caller, tt.portfolio.ID, first.ID)
		if err != nil || got.ID != first.ID {
			t.Fatalf("Get() = %+v, %v; want the first trade", got, err)
		}
		listed, err := tt.svc.List(t.Context(), tt.caller, tt.portfolio.ID, filter)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(listed.Items) != 2 || listed.Items[0].ID != second.ID || len(tt.transactions.filters) != 1 || tt.transactions.filters[0] != filter {
			t.Errorf("List() = %+v, filters passed = %+v; want both trades newest first and the filter passed on", listed, tt.transactions.filters)
		}
		if want := []string{"Transactions.Get", "Transactions.List"}; !slices.Equal(tt.log.calls, want) {
			t.Errorf("calls = %v, want %v outside any transaction", tt.log.calls, want)
		}
		if tt.uow.runs != 0 || len(tt.audit.events) != 0 {
			t.Errorf("transactions = %d, audit events = %d; reading writes nothing", tt.uow.runs, len(tt.audit.events))
		}
	})
	t.Run("a foreign portfolio reads as missing and is not recorded", func(t *testing.T) {
		tt := newTestTransactions(t)
		theirs := tt.seedPortfolio(t, mustID(t))
		tt.portfolios.existsAnswe = true

		if _, err := tt.svc.Get(t.Context(), tt.caller, theirs.ID, mustID(t)); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Get() error = %v, want not_found", err)
		}
		if _, err := tt.svc.List(t.Context(), tt.caller, theirs.ID, filter); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("List() error = %v, want not_found", err)
		}
		if slices.Contains(tt.log.calls, "Portfolios.Exists") || tt.uow.runs != 0 || len(tt.audit.events) != 0 {
			t.Errorf("calls = %v, transactions = %d, events = %d; a read is never an audited denial", tt.log.calls, tt.uow.runs, len(tt.audit.events))
		}
	})
}
