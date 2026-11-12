package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// valTransactions serves the history of one portfolio, as TransactionRepository.History does: the
// whole history in one call, or domain.ErrNotFound for a portfolio that is not the caller's.
type valTransactions struct {
	port.TransactionRepository
	history []domain.Transaction
	err     error
	calls   int
	owners  []domain.ID
}

func (f *valTransactions) History(_ context.Context, _, _ domain.ID) ([]domain.Transaction, error) {
	f.calls++
	return f.history, f.err
}

// OwnerHistory serves the same history as the trades of every portfolio of the owner.
func (f *valTransactions) OwnerHistory(_ context.Context, owner domain.ID) ([]domain.Transaction, error) {
	f.calls++
	f.owners = append(f.owners, owner)
	return f.history, f.err
}

// valPortfolios serves the portfolios of one user, as PortfolioRepository.ListAllOwned does.
type valPortfolios struct {
	port.PortfolioRepository
	all    []domain.Portfolio
	err    error
	owners []domain.ID
}

func (f *valPortfolios) ListAllOwned(_ context.Context, owner domain.ID) ([]domain.Portfolio, error) {
	f.owners = append(f.owners, owner)
	return f.all, f.err
}

type valInstruments struct {
	port.InstrumentRepository
	names map[domain.ID]string
	asked [][]domain.ID
}

func (f *valInstruments) ByIDs(_ context.Context, ids []domain.ID) (map[domain.ID]domain.Instrument, error) {
	f.asked = append(f.asked, ids)
	out := map[domain.ID]domain.Instrument{}
	for _, id := range ids {
		if name, ok := f.names[id]; ok {
			out[id] = domain.Instrument{ID: id, Name: name}
		}
	}
	return out, nil
}

type valStore struct {
	port.Repositories
	transactions *valTransactions
	instruments  *valInstruments
	portfolios   *valPortfolios
}

func (s valStore) Portfolios() port.PortfolioRepository     { return s.portfolios }
func (s valStore) Transactions() port.TransactionRepository { return s.transactions }
func (s valStore) Instruments() port.InstrumentRepository   { return s.instruments }

// valPrices records how the service asked for prices.
type valPrices struct {
	batch    domain.QuoteBatch
	err      error
	block    bool
	calls    int
	symbols  []domain.Symbol
	cutoff   time.Time
	deadline time.Time
}

// DailyBars is not used by valuation.
func (f *valPrices) DailyBars(context.Context, []domain.Symbol, domain.TradingDate, domain.TradingDate) (domain.DailyBarBatch, error) {
	return domain.DailyBarBatch{}, errors.New("valPrices: daily bars are not part of valuation")
}

func (f *valPrices) Quotes(ctx context.Context, symbols []domain.Symbol, cutoff time.Time) (domain.QuoteBatch, error) {
	f.calls++
	f.symbols, f.cutoff = symbols, cutoff
	f.deadline, _ = ctx.Deadline()
	if f.block {
		<-ctx.Done() // a provider that hangs until its context ends
		return domain.QuoteBatch{}, ctx.Err()
	}
	if f.err != nil {
		return domain.QuoteBatch{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return domain.QuoteBatch{}, err
	}
	return f.batch, nil
}

type testValuation struct {
	svc          *Valuation
	transactions *valTransactions
	instruments  *valInstruments
	portfolios   *valPortfolios
	prices       *valPrices
	clock        *fakeClock
	logs         *bytes.Buffer
	caller       domain.Principal
	portfolio    domain.ID
}

func newTestValuation(t *testing.T) testValuation {
	t.Helper()
	tv := testValuation{
		transactions: &valTransactions{}, instruments: &valInstruments{names: map[domain.ID]string{}},
		portfolios: &valPortfolios{}, prices: &valPrices{}, clock: newFakeClock(), logs: &bytes.Buffer{},
		caller: domain.Principal{UserID: mustID(t), SessionID: mustID(t)}, portfolio: mustID(t),
	}
	svc, err := NewValuation(ValuationDeps{
		Store: valStore{transactions: tv.transactions, instruments: tv.instruments, portfolios: tv.portfolios}, Prices: tv.prices, Clock: tv.clock,
		Logger: slog.New(slog.NewJSONHandler(tv.logs, nil)),
	})
	if err != nil {
		t.Fatalf("NewValuation() error = %v", err)
	}
	tv.svc = svc
	return tv
}

// trade builds one stored transaction of an instrument.
func valTrade(t *testing.T, id domain.ID, symbol string, side domain.Side, qty, price string) domain.Transaction {
	t.Helper()
	sym, err := domain.ParseSymbol(symbol)
	if err != nil {
		t.Fatal(err)
	}
	q, err := domain.ParseQuantity(qty)
	if err != nil {
		t.Fatal(err)
	}
	p, err := domain.ParsePrice(price)
	if err != nil {
		t.Fatal(err)
	}
	fee, err := domain.ParseFee("0")
	if err != nil {
		t.Fatal(err)
	}
	return domain.Transaction{InstrumentID: id, Symbol: sym, Side: side, Quantity: q, Price: p, Fee: fee}
}

func valQuote(t *testing.T, symbol, price string) domain.PriceQuote {
	t.Helper()
	sym, err := domain.ParseSymbol(symbol)
	if err != nil {
		t.Fatal(err)
	}
	p, err := domain.ParsePrice(price)
	if err != nil {
		t.Fatal(err)
	}
	return domain.PriceQuote{Symbol: sym, Price: p, Currency: "USD"}
}

func symbolNames(symbols []domain.Symbol) []string {
	out := make([]string, len(symbols))
	for i, s := range symbols {
		out[i] = s.String()
	}
	return out
}

func TestValueAsksTheProviderOnceForEveryOpenSymbol(t *testing.T) {
	tv := newTestValuation(t)
	aapl, msft, nvda, googl := mustID(t), mustID(t), mustID(t), mustID(t)
	tv.transactions.history = []domain.Transaction{
		valTrade(t, nvda, "NVDA", domain.SideBuy, "1", "10"),
		valTrade(t, googl, "GOOGL", domain.SideBuy, "1", "10"),
		valTrade(t, googl, "GOOGL", domain.SideSell, "1", "12"),
		valTrade(t, msft, "MSFT", domain.SideBuy, "2", "10"),
		valTrade(t, aapl, "AAPL", domain.SideBuy, "1", "10"),
	}
	tv.prices.batch = domain.QuoteBatch{Quotes: map[string]domain.PriceQuote{"AAPL": valQuote(t, "AAPL", "11")}}

	got, err := tv.svc.Value(t.Context(), tv.caller, tv.portfolio)
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	if tv.prices.calls != 1 {
		t.Fatalf("the provider was called %d times, want once for the whole portfolio", tv.prices.calls)
	}
	if names := symbolNames(tv.prices.symbols); strings.Join(names, ",") != "AAPL,MSFT,NVDA" {
		t.Errorf("symbols asked = %v, want the open ones [AAPL MSFT NVDA] and not the sold-out GOOGL", names)
	}
	if want := tv.clock.Now().Add(-15 * time.Minute); !tv.prices.cutoff.Equal(want) {
		t.Errorf("cutoff = %v, want now - 15 minutes = %v", tv.prices.cutoff, want)
	}
	if !got.CalculatedAt.Equal(tv.clock.Now()) || got.PortfolioID != tv.portfolio || got.Coverage != domain.CoveragePartial {
		t.Errorf("valuation = %+v", got)
	}
}

func TestValueSurvivesAProviderFailure(t *testing.T) {
	tv := newTestValuation(t)
	id := mustID(t)
	tv.transactions.history = []domain.Transaction{
		valTrade(t, id, "AAPL", domain.SideBuy, "2", "100"),
		valTrade(t, id, "AAPL", domain.SideSell, "1", "130"),
	}
	tv.prices.err = errors.New("bad key SECRET123 for account ACC-9")

	got, err := tv.svc.Value(t.Context(), tv.caller, tv.portfolio)
	if err != nil {
		t.Fatalf("Value() error = %v, want the valuation without prices", err)
	}
	if got.Coverage != domain.CoverageUnavailable || got.MarketValue != nil || got.UnrealizedPnL != nil {
		t.Fatalf("valuation = %+v, want unavailable with no totals", got)
	}
	if got.RealizedPnL.String() != "30" {
		t.Errorf("realized = %s, want 30 (it does not depend on prices)", got.RealizedPnL)
	}
	if p := got.Positions[0]; p.Reason != domain.ReasonProviderUnavailable || p.Quote != nil {
		t.Errorf("position = %+v, want provider_unavailable and no quote", p)
	}
	logs := tv.logs.String()
	if !strings.Contains(logs, `"event":"valuation.prices_failed"`) || !strings.Contains(logs, `"level":"WARN"`) {
		t.Errorf("log %q should record valuation.prices_failed at warn level", logs)
	}
	if strings.Contains(logs, "SECRET123") || strings.Contains(logs, "ACC-9") {
		t.Errorf("log %q repeats the provider's error text", logs)
	}
}

func TestValueStopsWhenTheRequestIsCancelled(t *testing.T) {
	tv := newTestValuation(t)
	tv.transactions.history = []domain.Transaction{valTrade(t, mustID(t), "AAPL", domain.SideBuy, "1", "10")}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := tv.svc.Value(ctx, tv.caller, tv.portfolio)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Value() error = %v, want the cancellation, not a valuation without prices", err)
	}
}

func TestEmptyAndClosedPortfoliosNeverCallTheProvider(t *testing.T) {
	closed := mustID(t)
	for name, history := range map[string][]domain.Transaction{
		"empty": nil,
		"closed": {
			valTrade(t, closed, "AAPL", domain.SideBuy, "1", "10"),
			valTrade(t, closed, "AAPL", domain.SideSell, "1", "12"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			tv := newTestValuation(t)
			tv.transactions.history = history
			got, err := tv.svc.Value(t.Context(), tv.caller, tv.portfolio)
			if err != nil || got.Coverage != domain.CoverageComplete {
				t.Fatalf("Value() = %+v, %v; want complete", got, err)
			}
			if tv.prices.calls != 0 {
				t.Errorf("the provider was called %d times for a portfolio that holds nothing", tv.prices.calls)
			}
		})
	}
}

func TestValueGivesTheProviderAtMostTenSeconds(t *testing.T) {
	tv := newTestValuation(t)
	tv.transactions.history = []domain.Transaction{valTrade(t, mustID(t), "AAPL", domain.SideBuy, "1", "10")}

	if _, err := tv.svc.Value(t.Context(), tv.caller, tv.portfolio); err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	left := time.Until(tv.prices.deadline)
	if tv.prices.deadline.IsZero() || left > 10*time.Second || left < 9*time.Second {
		t.Errorf("the provider's context has %v left, want just under 10s", left)
	}
}

func TestHoldingsNeverCallTheProvider(t *testing.T) {
	tv := newTestValuation(t)
	id := mustID(t)
	tv.instruments.names[id] = "Apple Inc."
	tv.transactions.history = []domain.Transaction{valTrade(t, id, "AAPL", domain.SideBuy, "2", "100")}

	got, err := tv.svc.Holdings(t.Context(), tv.caller, tv.portfolio)
	if err != nil {
		t.Fatalf("Holdings() error = %v", err)
	}
	if tv.prices.calls != 0 {
		t.Errorf("Holdings called the provider %d times", tv.prices.calls)
	}
	if len(got) != 1 || got[0].Symbol.String() != "AAPL" || got[0].InstrumentName != "Apple Inc." || got[0].Quantity.String() != "2" {
		t.Errorf("Holdings() = %+v", got)
	}
	if len(tv.instruments.asked) != 1 || len(tv.instruments.asked[0]) != 1 || tv.instruments.asked[0][0] != id {
		t.Errorf("instrument names were asked for %v, want once for the one instrument", tv.instruments.asked)
	}
}

func TestValuationOfAnotherUsersPortfolioIsNotFound(t *testing.T) {
	tv := newTestValuation(t)
	tv.transactions.err = domain.ErrNotFound

	_, errValue := tv.svc.Value(t.Context(), tv.caller, tv.portfolio)
	_, errHoldings := tv.svc.Holdings(t.Context(), tv.caller, tv.portfolio)

	if !errors.Is(errValue, domain.ErrNotFound) || !errors.Is(errHoldings, domain.ErrNotFound) {
		t.Fatalf("errors = %v / %v, want not_found", errValue, errHoldings)
	}
	if tv.prices.calls != 0 || len(tv.instruments.asked) != 0 {
		t.Errorf("provider calls = %d, instrument lookups = %d; want none for a portfolio that is not the caller's", tv.prices.calls, len(tv.instruments.asked))
	}
}

func TestCorruptHistoryIsInternal(t *testing.T) {
	tv := newTestValuation(t)
	tv.transactions.history = []domain.Transaction{valTrade(t, mustID(t), "AAPL", domain.SideSell, "1", "10")}

	for name, call := range map[string]func() error{
		"Value":    func() error { _, err := tv.svc.Value(t.Context(), tv.caller, tv.portfolio); return err },
		"Holdings": func() error { _, err := tv.svc.Holdings(t.Context(), tv.caller, tv.portfolio); return err },
	} {
		if err := call(); !errors.Is(err, domain.ErrInternal) {
			t.Errorf("%s() error = %v, want internal (stored history that sells more than it holds)", name, err)
		}
	}
	logs := tv.logs.String()
	if !strings.Contains(logs, `"event":"valuation.history_invalid"`) || !strings.Contains(logs, `"level":"ERROR"`) {
		t.Errorf("log %q should record valuation.history_invalid at error level", logs)
	}
	if strings.Contains(logs, "AAPL") {
		t.Errorf("log %q names a symbol of the user's portfolio", logs)
	}
}

func TestValuationNeedsACaller(t *testing.T) {
	tv := newTestValuation(t)
	var nobody domain.Principal

	_, errValue := tv.svc.Value(t.Context(), nobody, tv.portfolio)
	_, errHoldings := tv.svc.Holdings(t.Context(), nobody, tv.portfolio)

	if !errors.Is(errValue, domain.ErrUnauthenticated) || !errors.Is(errHoldings, domain.ErrUnauthenticated) {
		t.Fatalf("errors = %v / %v, want unauthenticated", errValue, errHoldings)
	}
	if tv.transactions.calls != 0 || tv.prices.calls != 0 {
		t.Errorf("repositories touched: history %d, prices %d", tv.transactions.calls, tv.prices.calls)
	}
}

func TestNewValuationNeedsEveryDependency(t *testing.T) {
	good := ValuationDeps{
		Store: valStore{}, Prices: &valPrices{}, Clock: newFakeClock(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	if _, err := NewValuation(good); err != nil {
		t.Fatalf("NewValuation(valid) error = %v", err)
	}
	for name, mutate := range map[string]func(*ValuationDeps){
		"store":  func(d *ValuationDeps) { d.Store = nil },
		"prices": func(d *ValuationDeps) { d.Prices = nil },
		"clock":  func(d *ValuationDeps) { d.Clock = nil },
		"logger": func(d *ValuationDeps) { d.Logger = nil },
	} {
		d := good
		mutate(&d)
		if _, err := NewValuation(d); err == nil {
			t.Errorf("NewValuation(no %s) error = nil", name)
		}
	}
}

// The request has a deadline of its own (12 s). When it, and not the provider's 10 s, is what ends
// the wait, the valuation must still be a 200 without prices, not a bare deadline error (a 500).
func TestValueWithoutPricesWhenTheRequestDeadlineEndsTheWait(t *testing.T) {
	tv := newTestValuation(t)
	id := mustID(t)
	tv.transactions.history = []domain.Transaction{
		valTrade(t, id, "AAPL", domain.SideBuy, "2", "100"),
		valTrade(t, id, "AAPL", domain.SideSell, "1", "130"),
	}
	tv.prices.block = true
	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()

	start := time.Now()
	got, err := tv.svc.Value(ctx, tv.caller, tv.portfolio)

	if err != nil {
		t.Fatalf("Value() error = %v, want a valuation without prices", err)
	}
	if got.Coverage != domain.CoverageUnavailable || got.Positions[0].Reason != domain.ReasonProviderUnavailable || got.RealizedPnL.String() != "30" {
		t.Errorf("valuation = %+v, want unavailable, provider_unavailable and realized 30", got)
	}
	if took := time.Since(start); took > 1400*time.Millisecond {
		t.Errorf("Value() took %v, want it to give up before the request deadline and leave time to answer", took)
	}
}

func TestValueSkipsTheProviderWhenTheRequestHasNoTimeLeft(t *testing.T) {
	tv := newTestValuation(t)
	tv.transactions.history = []domain.Transaction{valTrade(t, mustID(t), "AAPL", domain.SideBuy, "1", "10")}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond) // less than the margin kept for the answer
	defer cancel()

	got, err := tv.svc.Value(ctx, tv.caller, tv.portfolio)

	if err != nil || got.Coverage != domain.CoverageUnavailable || got.Positions[0].Reason != domain.ReasonProviderUnavailable {
		t.Fatalf("Value() = %+v, %v; want an unavailable valuation", got, err)
	}
	if tv.prices.calls != 0 {
		t.Errorf("the provider was called %d times with no time left to wait for it", tv.prices.calls)
	}
}

// valPortfolio adds a portfolio of the caller to the fake store and returns it.
func (tv *testValuation) valPortfolio(t *testing.T, name string) domain.Portfolio {
	t.Helper()
	n, err := domain.ParsePortfolioName(name)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.Portfolio{ID: mustID(t), UserID: tv.caller.UserID, Name: n, Currency: domain.CurrencyUSD}
	tv.portfolios.all = append(tv.portfolios.all, p)
	return p
}

// in puts a trade in a portfolio.
func in(p domain.Portfolio, trade domain.Transaction) domain.Transaction {
	trade.PortfolioID = p.ID
	return trade
}

func TestValueAllAsksTheProviderOnceForTheSymbolsOfEveryPortfolio(t *testing.T) {
	tv := newTestValuation(t)
	long, short := tv.valPortfolio(t, "Long term"), tv.valPortfolio(t, "Short term")
	aapl, msft, nvda := mustID(t), mustID(t), mustID(t)
	tv.instruments.names[aapl] = "Apple Inc."
	tv.transactions.history = []domain.Transaction{
		in(long, valTrade(t, nvda, "NVDA", domain.SideBuy, "1", "10")),
		in(long, valTrade(t, nvda, "NVDA", domain.SideSell, "1", "12")), // sold out: not asked about
		in(long, valTrade(t, aapl, "AAPL", domain.SideBuy, "2", "100")),
		in(short, valTrade(t, msft, "MSFT", domain.SideBuy, "1", "50")),
		in(short, valTrade(t, aapl, "AAPL", domain.SideBuy, "3", "120")),
	}
	tv.prices.batch = domain.QuoteBatch{Quotes: map[string]domain.PriceQuote{
		"AAPL": valQuote(t, "AAPL", "150"), "MSFT": valQuote(t, "MSFT", "50"),
	}}

	got, err := tv.svc.ValueAll(t.Context(), tv.caller)
	if err != nil {
		t.Fatalf("ValueAll() error = %v", err)
	}
	if tv.prices.calls != 1 {
		t.Fatalf("the provider was called %d times, want once for the whole account", tv.prices.calls)
	}
	if names := symbolNames(tv.prices.symbols); strings.Join(names, ",") != "AAPL,MSFT" {
		t.Errorf("symbols asked = %v, want each open symbol once: [AAPL MSFT]", names)
	}
	if want := tv.clock.Now().Add(-15 * time.Minute); !tv.prices.cutoff.Equal(want) {
		t.Errorf("cutoff = %v, want now - 15 minutes = %v", tv.prices.cutoff, want)
	}
	if tv.transactions.calls != 1 || len(tv.transactions.owners) != 1 || tv.transactions.owners[0] != tv.caller.UserID ||
		len(tv.portfolios.owners) != 1 || tv.portfolios.owners[0] != tv.caller.UserID {
		t.Errorf("store was asked for owners %v / %v, want once each for the caller", tv.portfolios.owners, tv.transactions.owners)
	}
	if len(tv.instruments.asked) != 1 || len(tv.instruments.asked[0]) != 3 {
		t.Errorf("instrument names were asked for %v, want once for the three instruments", tv.instruments.asked)
	}

	// Long: 2 AAPL = 300 on 200. Short: 3 AAPL = 450 on 360, 1 MSFT = 50 on 50. All: 800 on 610.
	if !got.CalculatedAt.Equal(tv.clock.Now()) || got.Coverage != domain.CoverageComplete ||
		got.MarketValue == nil || got.MarketValue.String() != "800" || got.CostBasis.String() != "610" || got.RealizedPnL.String() != "2" {
		t.Fatalf("account = %+v", got)
	}
	if len(got.Portfolios) != 2 || got.Portfolios[0].Portfolio.ID != long.ID || got.Portfolios[1].Portfolio.ID != short.ID ||
		got.Portfolios[0].Valuation.MarketValue.String() != "300" || got.Portfolios[1].Valuation.MarketValue.String() != "500" ||
		got.Portfolios[0].Valuation.PortfolioID != long.ID {
		t.Errorf("portfolios = %+v, want Long (300) then Short (500)", got.Portfolios)
	}
	if len(got.Allocation) != 2 || got.Allocation[0].Symbol.String() != "AAPL" || got.Allocation[0].Quantity.String() != "5" ||
		got.Allocation[0].MarketValue.String() != "750" || got.Allocation[0].InstrumentName != "Apple Inc." {
		t.Errorf("allocation = %+v, want AAPL merged over both portfolios (5 shares, 750) first", got.Allocation)
	}
}

func TestValueAllIgnoresTradesOfAPortfolioThatIsNotListed(t *testing.T) {
	// A portfolio created between the two reads has trades but is not in the list: it is left out as
	// a whole, never half counted.
	tv := newTestValuation(t)
	listed := tv.valPortfolio(t, "Listed")
	id := mustID(t)
	unlisted := valTrade(t, id, "AAPL", domain.SideBuy, "7", "100")
	unlisted.PortfolioID = mustID(t)
	tv.transactions.history = []domain.Transaction{in(listed, valTrade(t, id, "AAPL", domain.SideBuy, "1", "100")), unlisted}
	tv.prices.batch = domain.QuoteBatch{Quotes: map[string]domain.PriceQuote{"AAPL": valQuote(t, "AAPL", "110")}}

	got, err := tv.svc.ValueAll(t.Context(), tv.caller)
	if err != nil {
		t.Fatalf("ValueAll() error = %v", err)
	}
	if len(got.Portfolios) != 1 || got.MarketValue == nil || got.MarketValue.String() != "110" || got.Allocation[0].Quantity.String() != "1" {
		t.Errorf("account = %+v, want only the listed portfolio's one share", got)
	}
}

func TestValueAllSurvivesAProviderFailure(t *testing.T) {
	tv := newTestValuation(t)
	p := tv.valPortfolio(t, "Long term")
	id := mustID(t)
	tv.transactions.history = []domain.Transaction{
		in(p, valTrade(t, id, "AAPL", domain.SideBuy, "2", "100")),
		in(p, valTrade(t, id, "AAPL", domain.SideSell, "1", "130")),
	}
	tv.prices.err = errors.New("bad key SECRET123 for account ACC-9")

	got, err := tv.svc.ValueAll(t.Context(), tv.caller)
	if err != nil {
		t.Fatalf("ValueAll() error = %v, want the summary without prices", err)
	}
	if got.Coverage != domain.CoverageUnavailable || got.MarketValue != nil || got.RealizedPnL.String() != "30" {
		t.Fatalf("account = %+v, want unavailable, no total and realized 30", got)
	}
	logs := tv.logs.String()
	if !strings.Contains(logs, `"event":"valuation.prices_failed"`) || strings.Contains(logs, "SECRET123") {
		t.Errorf("log %q should record valuation.prices_failed without the provider's text", logs)
	}
}

func TestValueAllWithNothingHeldNeverCallsTheProvider(t *testing.T) {
	for name, withPortfolio := range map[string]bool{"no portfolio": false, "an empty portfolio": true} {
		t.Run(name, func(t *testing.T) {
			tv := newTestValuation(t)
			if withPortfolio {
				tv.valPortfolio(t, "Empty")
			}
			got, err := tv.svc.ValueAll(t.Context(), tv.caller)
			if err != nil || got.Coverage != domain.CoverageComplete || got.MarketValue == nil || !got.MarketValue.IsZero() {
				t.Fatalf("ValueAll() = %+v, %v; want complete and worth zero", got, err)
			}
			if tv.prices.calls != 0 {
				t.Errorf("the provider was called %d times for an account that holds nothing", tv.prices.calls)
			}
			if !got.CalculatedAt.Equal(tv.clock.Now()) {
				t.Errorf("calculated at %v, want the clock %v", got.CalculatedAt, tv.clock.Now())
			}
		})
	}
}

func TestValueAllNeedsAPrincipal(t *testing.T) {
	tv := newTestValuation(t)
	if _, err := tv.svc.ValueAll(t.Context(), domain.Principal{}); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("ValueAll(no principal) error = %v, want unauthenticated", err)
	}
	if len(tv.portfolios.owners) != 0 || tv.transactions.calls != 0 {
		t.Error("the store was read without a principal")
	}
}

func TestValueAllReportsStoreErrorsAndDamagedHistory(t *testing.T) {
	down := errors.New("database is down")
	t.Run("portfolios", func(t *testing.T) {
		tv := newTestValuation(t)
		tv.portfolios.err = down
		if _, err := tv.svc.ValueAll(t.Context(), tv.caller); !errors.Is(err, down) {
			t.Errorf("error = %v, want the store's", err)
		}
	})
	t.Run("history", func(t *testing.T) {
		tv := newTestValuation(t)
		tv.valPortfolio(t, "A")
		tv.transactions.err = down
		if _, err := tv.svc.ValueAll(t.Context(), tv.caller); !errors.Is(err, down) {
			t.Errorf("error = %v, want the store's", err)
		}
	})
	t.Run("a sale of more than is held", func(t *testing.T) {
		tv := newTestValuation(t)
		p := tv.valPortfolio(t, "A")
		tv.transactions.history = []domain.Transaction{in(p, valTrade(t, mustID(t), "AAPL", domain.SideSell, "1", "10"))}
		if _, err := tv.svc.ValueAll(t.Context(), tv.caller); !errors.Is(err, domain.ErrInternal) {
			t.Errorf("error = %v, want internal: stored history that cannot be replayed is damaged data", err)
		}
		if tv.prices.calls != 0 {
			t.Error("the provider was asked about damaged history")
		}
	})
}

func TestValueAllStopsWhenTheRequestIsCancelled(t *testing.T) {
	tv := newTestValuation(t)
	p := tv.valPortfolio(t, "A")
	tv.transactions.history = []domain.Transaction{in(p, valTrade(t, mustID(t), "AAPL", domain.SideBuy, "1", "10"))}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := tv.svc.ValueAll(ctx, tv.caller); !errors.Is(err, context.Canceled) {
		t.Fatalf("ValueAll() error = %v, want the cancellation", err)
	}
}
