package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// As in position_test.go, every expected value is worked out by hand, not taken from the code.

var valuedAt = time.Date(2026, time.November, 4, 15, 0, 0, 0, time.UTC)

func symbolOf(t *testing.T, s string) Symbol {
	t.Helper()
	sym, err := ParseSymbol(s)
	if err != nil {
		t.Fatal(err)
	}
	return sym
}

// tradeIn is txOf for a named instrument.
func tradeIn(t *testing.T, id ID, symbol string, side Side, qty, price, fee string) Transaction {
	t.Helper()
	tx := txOf(t, id, side, qty, price, fee)
	tx.Symbol = symbolOf(t, symbol)
	return tx
}

func quoteOf(t *testing.T, symbol, price string) PriceQuote {
	t.Helper()
	p, err := ParsePrice(price)
	if err != nil {
		t.Fatal(err)
	}
	return PriceQuote{Symbol: symbolOf(t, symbol), Price: p, Currency: "USD"}
}

func batchOf(quotes ...PriceQuote) QuoteBatch {
	b := QuoteBatch{Quotes: map[string]PriceQuote{}}
	for _, q := range quotes {
		b.Quotes[q.Symbol.String()] = q
	}
	return b
}

// holdingsOf replays history and builds the holdings the way the service does.
func holdingsOf(t *testing.T, history []Transaction, names map[ID]string) []Holding {
	t.Helper()
	positions, err := ReplayHistory(history)
	if err != nil {
		t.Fatalf("ReplayHistory() error = %v", err)
	}
	return BuildHoldings(positions, history, names)
}

func requireDecimal(t *testing.T, label string, got decimal.Decimal, want string) {
	t.Helper()
	if got.String() != want {
		t.Errorf("%s = %s, want %s", label, got.String(), want)
	}
}

func requireOptional(t *testing.T, label string, got *decimal.Decimal, want string) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want %s", label, want)
		return
	}
	requireDecimal(t, label, *got, want)
}

func TestBuildHoldingsSortsAndKeepsClosedPositions(t *testing.T) {
	aapl, msft := mustNewID(t), mustNewID(t)
	history := []Transaction{
		tradeIn(t, msft, "MSFT", SideBuy, "3", "10", "0"),
		tradeIn(t, aapl, "AAPL", SideBuy, "1", "100", "0"),
		tradeIn(t, aapl, "AAPL", SideSell, "1", "108.5", "0"),
	}
	got := holdingsOf(t, history, map[ID]string{aapl: "Apple Inc."})

	if len(got) != 2 || got[0].Symbol.String() != "AAPL" || got[1].Symbol.String() != "MSFT" {
		t.Fatalf("holdings = %+v, want AAPL then MSFT", got)
	}
	requireDecimal(t, "AAPL quantity", got[0].Quantity, "0")
	requireDecimal(t, "AAPL realized", got[0].RealizedPnL, "8.5")
	requireDecimal(t, "MSFT quantity", got[1].Quantity, "3")
	if got[0].InstrumentName != "Apple Inc." || got[1].InstrumentName != "MSFT" {
		t.Errorf("names = %q, %q; want the given name and, when unknown, the symbol", got[0].InstrumentName, got[1].InstrumentName)
	}
	if got[0].InstrumentID != aapl {
		t.Errorf("InstrumentID = %v, want %v", got[0].InstrumentID, aapl)
	}
}

func TestBuildHoldingsUsesTheLatestSymbolOfAnInstrument(t *testing.T) {
	id := mustNewID(t)
	history := []Transaction{
		tradeIn(t, id, "FB", SideBuy, "1", "10", "0"),
		tradeIn(t, id, "META", SideBuy, "1", "10", "0"),
	}
	got := holdingsOf(t, history, nil)
	if len(got) != 1 || got[0].Symbol.String() != "META" {
		t.Fatalf("holdings = %+v, want one holding under the latest symbol META", got)
	}
}

func TestValueGoldenExample(t *testing.T) {
	// Spec section 12: buy 2 x 100 + fee 2 -> cost 202; sell 0.5 x 120 - fee 1 -> allocated 50.5,
	// realized 8.5, 1.5 shares, cost 151.5. At 110: market value 165, unrealized 13.5.
	id := mustNewID(t)
	holdings := holdingsOf(t, []Transaction{
		tradeIn(t, id, "AAPL", SideBuy, "2", "100", "2"),
		tradeIn(t, id, "AAPL", SideSell, "0.5", "120", "1"),
	}, map[ID]string{id: "Apple Inc."})
	portfolio := mustNewID(t)

	got := ValuePortfolio(portfolio, valuedAt, holdings, batchOf(quoteOf(t, "AAPL", "110")), false)

	if got.PortfolioID != portfolio || !got.CalculatedAt.Equal(valuedAt) || got.Coverage != CoverageComplete {
		t.Fatalf("valuation = %+v", got)
	}
	requireOptional(t, "market value", got.MarketValue, "165")
	requireOptional(t, "unrealized", got.UnrealizedPnL, "13.5")
	requireDecimal(t, "realized", got.RealizedPnL, "8.5")
	requireDecimal(t, "priced market value", got.PricedMarketValue, "165")
	if got.MissingSymbols == nil || len(got.MissingSymbols) != 0 {
		t.Errorf("MissingSymbols = %#v, want an empty, non-nil slice", got.MissingSymbols)
	}
	p := got.Positions[0]
	requireDecimal(t, "quantity", p.Holding.Quantity, "1.5")
	requireDecimal(t, "cost basis", p.Holding.CostBasis, "151.5")
	requireDecimal(t, "average cost", p.Holding.AverageCost, "101")
	requireOptional(t, "position market value", p.MarketValue, "165")
	requireOptional(t, "position unrealized", p.UnrealizedPnL, "13.5")
	if p.Quote == nil || p.Quote.Price.String() != "110" || p.Reason != "" {
		t.Errorf("position quote = %+v, reason %q", p.Quote, p.Reason)
	}
}

func TestValuePartiallyPricedPortfolio(t *testing.T) {
	aapl, msft, nvda := mustNewID(t), mustNewID(t), mustNewID(t)
	holdings := holdingsOf(t, []Transaction{
		tradeIn(t, aapl, "AAPL", SideBuy, "2", "100", "2"),
		tradeIn(t, aapl, "AAPL", SideSell, "0.5", "120", "1"),
		tradeIn(t, nvda, "NVDA", SideBuy, "1", "50", "0"),
		tradeIn(t, msft, "MSFT", SideBuy, "1", "30", "0"),
	}, nil)

	got := ValuePortfolio(mustNewID(t), valuedAt, holdings, batchOf(quoteOf(t, "AAPL", "110")), false)

	if got.Coverage != CoveragePartial || got.MarketValue != nil || got.UnrealizedPnL != nil {
		t.Fatalf("coverage = %q, market value = %v, unrealized = %v; want partial with both totals nil", got.Coverage, got.MarketValue, got.UnrealizedPnL)
	}
	requireDecimal(t, "priced market value", got.PricedMarketValue, "165")
	requireDecimal(t, "realized", got.RealizedPnL, "8.5")
	if len(got.MissingSymbols) != 2 || got.MissingSymbols[0].String() != "MSFT" || got.MissingSymbols[1].String() != "NVDA" {
		t.Errorf("MissingSymbols = %v, want [MSFT NVDA]", got.MissingSymbols)
	}
	var msftPosition PositionValuation
	for _, p := range got.Positions {
		if p.Holding.Symbol.String() == "MSFT" {
			msftPosition = p
		}
	}
	if msftPosition.Quote != nil || msftPosition.MarketValue != nil || msftPosition.UnrealizedPnL != nil || msftPosition.Reason != ReasonNoPrice {
		t.Errorf("MSFT position = %+v, want no quote, no value and reason no_price", msftPosition)
	}
}

func TestValueWithNoPricesIsUnavailable(t *testing.T) {
	a, b := mustNewID(t), mustNewID(t)
	holdings := holdingsOf(t, []Transaction{
		tradeIn(t, a, "AAPL", SideBuy, "1", "100", "0"),
		tradeIn(t, a, "AAPL", SideSell, "0.25", "120", "0"),
		tradeIn(t, b, "MSFT", SideBuy, "2", "30", "0"),
	}, nil)

	for name, tc := range map[string]struct {
		batch  QuoteBatch
		failed bool
		reason UnavailableReason
	}{
		"the provider has no price":          {QuoteBatch{Unavailable: []Symbol{symbolOf(t, "AAPL"), symbolOf(t, "MSFT")}}, false, ReasonNoPrice},
		"the provider failed":                {QuoteBatch{}, true, ReasonProviderUnavailable},
		"quotes are ignored after a failure": {batchOf(quoteOf(t, "AAPL", "110")), true, ReasonProviderUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			got := ValuePortfolio(mustNewID(t), valuedAt, holdings, tc.batch, tc.failed)
			if got.Coverage != CoverageUnavailable || got.MarketValue != nil || got.UnrealizedPnL != nil {
				t.Fatalf("coverage = %q, totals = %v / %v; want unavailable with nil totals", got.Coverage, got.MarketValue, got.UnrealizedPnL)
			}
			requireDecimal(t, "priced market value", got.PricedMarketValue, "0")
			requireDecimal(t, "realized", got.RealizedPnL, "5") // 0.25 x 120 - 0.25 x 100
			for _, p := range got.Positions {
				if p.Reason != tc.reason || p.Quote != nil || p.MarketValue != nil || p.UnrealizedPnL != nil {
					t.Errorf("position %s = %+v, want reason %q and nothing priced", p.Holding.Symbol, p, tc.reason)
				}
			}
		})
	}
}

func TestValueEmptyAndClosedPortfoliosAreCompleteWithZeros(t *testing.T) {
	id := mustNewID(t)
	closed := holdingsOf(t, []Transaction{
		tradeIn(t, id, "AAPL", SideBuy, "1", "100", "0"),
		tradeIn(t, id, "AAPL", SideSell, "1", "108.5", "0"),
	}, nil)

	for name, holdings := range map[string][]Holding{"empty": nil, "closed": closed} {
		t.Run(name, func(t *testing.T) {
			got := ValuePortfolio(mustNewID(t), valuedAt, holdings, QuoteBatch{}, false)
			if got.Coverage != CoverageComplete {
				t.Fatalf("coverage = %q, want complete", got.Coverage)
			}
			requireOptional(t, "market value", got.MarketValue, "0")
			requireOptional(t, "unrealized", got.UnrealizedPnL, "0")
			requireDecimal(t, "priced market value", got.PricedMarketValue, "0")
			if got.Positions == nil || len(got.Positions) != len(holdings) {
				t.Errorf("Positions = %#v, want a non-nil slice with one entry per holding", got.Positions)
			}
			for _, p := range got.Positions {
				requireOptional(t, "closed market value", p.MarketValue, "0")
				requireOptional(t, "closed unrealized", p.UnrealizedPnL, "0")
				if p.Quote != nil || p.Reason != "" {
					t.Errorf("closed position = %+v, want no quote and no reason", p)
				}
			}
			if name == "closed" {
				requireDecimal(t, "realized of the closed position", got.RealizedPnL, "8.5")
			}
		})
	}
}

func TestValueDoesNotRoundProducts(t *testing.T) {
	id := mustNewID(t)
	holdings := holdingsOf(t, []Transaction{tradeIn(t, id, "AAPL", SideBuy, "0.0000000001", "123", "0")}, nil)

	got := ValuePortfolio(mustNewID(t), valuedAt, holdings, batchOf(quoteOf(t, "AAPL", "123.4567890123")), false)

	// 1e-10 x 123.4567890123 = 1.234567890123e-8 and the cost is 1e-10 x 123 = 1.23e-8.
	requireOptional(t, "market value", got.MarketValue, "0.0000000123456789"+"0123")
	requireOptional(t, "unrealized", got.UnrealizedPnL, "0.0000000000"+"4567890123")
}

func TestHoldingsKeepTheAverageCostTheReplayComputed(t *testing.T) {
	id := mustNewID(t)
	holdings := holdingsOf(t, []Transaction{tradeIn(t, id, "AAPL", SideBuy, "3", "10", "0.01")}, nil)
	// 30.01 / 3 at 28 places: 10.0033333333333333333333333333
	if got, want := holdings[0].AverageCost.String(), "10."+"0033333333333333333333333333"; got != want {
		t.Errorf("average cost = %s, want %s", got, want)
	}
	if !strings.HasPrefix(holdings[0].CostBasis.String(), "30.01") {
		t.Errorf("cost basis = %s", holdings[0].CostBasis)
	}
}

func TestValueLosingPositionHasNegativeUnrealized(t *testing.T) {
	id := mustNewID(t)
	holdings := holdingsOf(t, []Transaction{tradeIn(t, id, "AAPL", SideBuy, "1", "100", "0")}, nil)
	got := ValuePortfolio(mustNewID(t), valuedAt, holdings, batchOf(quoteOf(t, "AAPL", "90")), false)
	requireOptional(t, "unrealized", got.UnrealizedPnL, "-10")
	requireOptional(t, "market value", got.MarketValue, "90")
}

func TestOpenSymbolsListsOnlyWhatIsStillHeld(t *testing.T) {
	a, b, c := mustNewID(t), mustNewID(t), mustNewID(t)
	holdings := holdingsOf(t, []Transaction{
		tradeIn(t, b, "MSFT", SideBuy, "1", "10", "0"),
		tradeIn(t, a, "AAPL", SideBuy, "1", "10", "0"),
		tradeIn(t, a, "AAPL", SideSell, "1", "10", "0"),
		tradeIn(t, c, "NVDA", SideBuy, "1", "10", "0"),
	}, nil)
	got := OpenSymbols(holdings)
	if len(got) != 2 || got[0].String() != "MSFT" || got[1].String() != "NVDA" {
		t.Errorf("OpenSymbols() = %v, want [MSFT NVDA]", got)
	}
	if got := OpenSymbols(nil); got == nil || len(got) != 0 {
		t.Errorf("OpenSymbols(nil) = %#v, want an empty, non-nil slice", got)
	}
}
