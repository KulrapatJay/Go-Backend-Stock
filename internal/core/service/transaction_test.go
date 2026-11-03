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

// fakeTransactions behaves like the database: Insert assigns the next sequence and version 1, and
// History returns every stored row ordered by (executed_at, sequence).
type fakeTransactions struct {
	port.TransactionRepository
	log       *callLog
	rows      []domain.Transaction
	nextSeq   int64
	inserted  []domain.Transaction // what the service passed to Insert
	insertErr error
	// stored, when set, changes the row Insert returns, as a database default or trigger would.
	stored func(*domain.Transaction)
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

func (f *fakeTransactions) History(_ context.Context, _, portfolioID domain.ID) ([]domain.Transaction, error) {
	f.log.add("Transactions.History")
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

type fakeTransactionKeys struct {
	port.IdempotencyRepository
	log     *callLog
	record  *domain.IdempotencyRecord
	gotKey  string
	deleted []time.Time
	saved   []domain.IdempotencyRecord
}

func (f *fakeTransactionKeys) Get(_ context.Context, _ domain.IdempotencyScope, key string) (domain.IdempotencyRecord, error) {
	f.log.add("Idempotency.Get")
	f.gotKey = key
	if f.record == nil {
		return domain.IdempotencyRecord{}, domain.ErrNotFound
	}
	return *f.record, nil
}

func (f *fakeTransactionKeys) DeleteExpired(_ context.Context, _ domain.IdempotencyScope, _ string, now time.Time) error {
	f.log.add("Idempotency.DeleteExpired")
	f.deleted = append(f.deleted, now)
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
// asked before the portfolio lock is taken.
type fakeProvider struct {
	log        *callLog
	uow        *transactionUoW
	answer     domain.Instrument
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
	return answer, nil
}

type transactionStore struct {
	port.Repositories
	portfolios  *fakePortfolios
	instruments *fakeInstruments
	limits      loggingLimits
}

func (s transactionStore) Portfolios() port.PortfolioRepository   { return s.portfolios }
func (s transactionStore) Instruments() port.InstrumentRepository { return s.instruments }
func (s transactionStore) RateLimits() port.RateLimitRepository   { return s.limits }

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

type transactionUoW struct {
	tx   transactionTx
	runs int
}

func (u *transactionUoW) WithinTransaction(_ context.Context, fn func(port.TxRepositories) error) error {
	u.runs++
	return fn(u.tx)
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
	transactions := &fakeTransactions{log: log}
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
		Store:      transactionStore{portfolios: portfolios, instruments: instruments, limits: loggingLimits{log: log, limits: limits, scopes: scopes}},
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

// seedTrade stores a trade the portfolio already has, as History would return it.
func (tt testTransactions) seedTrade(t *testing.T, in domain.TransactionInput) {
	t.Helper()
	tt.transactions.nextSeq++
	tt.transactions.rows = append(tt.transactions.rows, domain.Transaction{
		ID: mustID(t), PortfolioID: tt.portfolio.ID, InstrumentID: tt.aapl.ID, Symbol: tt.aapl.Symbol,
		Sequence: tt.transactions.nextSeq, Side: in.Side, Quantity: in.Quantity, Price: in.Price, Fee: in.Fee,
		ExecutedAt: in.ExecutedAt, Version: mustVersion(t, 1), CreatedAt: tt.clock.Now(), UpdatedAt: tt.clock.Now(),
	})
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
	if tt.uow.runs != 1 || len(tt.provider.runsAtCall) != 0 {
		t.Errorf("transactions = %d, provider calls = %d; want 1 and 0", tt.uow.runs, len(tt.provider.runsAtCall))
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
	wantScope := domain.IdempotencyScope{UserID: tt.caller.UserID, PortfolioID: tt.portfolio.ID, Operation: domain.IdempotencyOperationCreateTransaction}
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

func (tt testTransactions) storedRecord(t *testing.T, in domain.TransactionInput, createdAt time.Time, result domain.Transaction) *domain.IdempotencyRecord {
	t.Helper()
	raw, err := domain.MarshalTransactionSnapshot(result)
	if err != nil {
		t.Fatal(err)
	}
	return &domain.IdempotencyRecord{
		Scope:       domain.IdempotencyScope{UserID: tt.caller.UserID, PortfolioID: tt.portfolio.ID, Operation: domain.IdempotencyOperationCreateTransaction},
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
	if tt.keys.gotKey != "key-1" || len(tt.audit.events) != 0 || len(tt.provider.runsAtCall) != 0 {
		t.Errorf("key = %q, events = %d, provider calls = %d", tt.keys.gotKey, len(tt.audit.events), len(tt.provider.runsAtCall))
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
