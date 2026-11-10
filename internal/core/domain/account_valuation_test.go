package domain

import (
	"strings"
	"testing"
)

// As in valuation_test.go, every expected value is worked out by hand.

func portfolioNamed(t *testing.T, name string) Portfolio {
	t.Helper()
	n, err := ParsePortfolioName(name)
	if err != nil {
		t.Fatal(err)
	}
	return Portfolio{ID: mustNewID(t), Name: n, Currency: CurrencyUSD}
}

// valued values one portfolio from its history, the way the service does before it summarizes.
func valued(t *testing.T, p Portfolio, history []Transaction, names map[ID]string, batch QuoteBatch, failed bool) ValuedPortfolio {
	t.Helper()
	return ValuedPortfolio{Portfolio: p, Valuation: ValuePortfolio(p.ID, valuedAt, holdingsOf(t, history, names), batch, failed)}
}

func allocationSymbols(rows []Allocation) string {
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Symbol.String()
	}
	return strings.Join(names, ",")
}

func TestSummarizeAccountAddsPortfoliosAndMergesTheSameStock(t *testing.T) {
	// Long: 2 AAPL bought at 100 (cost 200), 1 MSFT at 50 (cost 50). Short: 3 AAPL at 120 (cost 360).
	// Prices: AAPL 150, MSFT 50.
	//   Long:  value 300 + 50 = 350, cost 250, unrealized 100 (40%)
	//   Short: value 450,           cost 360, unrealized  90 (25%)
	//   All:   value 800,           cost 610, unrealized 190 (190 / 610 x 100 = 31.1475...)
	//   AAPL across both: 5 shares worth 750 (93.75%); MSFT: 1 share worth 50 (6.25%).
	aapl, msft := mustNewID(t), mustNewID(t)
	names := map[ID]string{aapl: "Apple Inc.", msft: "Microsoft Corporation"}
	batch := batchOf(quoteOf(t, "AAPL", "150"), quoteOf(t, "MSFT", "50"))
	long, short := portfolioNamed(t, "Long term"), portfolioNamed(t, "Short term")

	got := SummarizeAccount(valuedAt, []ValuedPortfolio{
		valued(t, long, []Transaction{
			tradeIn(t, aapl, "AAPL", SideBuy, "2", "100", "0"),
			tradeIn(t, msft, "MSFT", SideBuy, "1", "50", "0"),
		}, names, batch, false),
		valued(t, short, []Transaction{tradeIn(t, aapl, "AAPL", SideBuy, "3", "120", "0")}, names, batch, false),
	})

	if !got.CalculatedAt.Equal(valuedAt) || got.Coverage != CoverageComplete {
		t.Fatalf("calculated at %v, coverage %q", got.CalculatedAt, got.Coverage)
	}
	requireDecimal(t, "cost basis", got.CostBasis, "610")
	requireOptional(t, "market value", got.MarketValue, "800")
	requireDecimal(t, "priced market value", got.PricedMarketValue, "800")
	requireOptional(t, "unrealized", got.UnrealizedPnL, "190")
	requireOptional(t, "unrealized percent", got.UnrealizedPnLPercent, "31.1475409836065573770491803279")
	if got.MissingSymbols == nil || len(got.MissingSymbols) != 0 {
		t.Errorf("MissingSymbols = %#v, want an empty, non-nil slice", got.MissingSymbols)
	}

	if len(got.Portfolios) != 2 || got.Portfolios[0].Portfolio.ID != long.ID || got.Portfolios[1].Portfolio.ID != short.ID {
		t.Fatalf("portfolios = %+v, want Long then Short, in the order given", got.Portfolios)
	}
	requireOptional(t, "Long weight", got.Portfolios[0].WeightPercent, "43.75")  // 350 / 800
	requireOptional(t, "Short weight", got.Portfolios[1].WeightPercent, "56.25") // 450 / 800
	requireOptional(t, "Long percent", got.Portfolios[0].Valuation.UnrealizedPnLPercent, "40")
	requireOptional(t, "Short percent", got.Portfolios[1].Valuation.UnrealizedPnLPercent, "25")

	if allocationSymbols(got.Allocation) != "AAPL,MSFT" {
		t.Fatalf("allocation = %s, want AAPL then MSFT (largest first)", allocationSymbols(got.Allocation))
	}
	a, m := got.Allocation[0], got.Allocation[1]
	if a.InstrumentID != aapl || a.InstrumentName != "Apple Inc." || a.Quote == nil || a.Quote.Price.String() != "150" || a.Reason != "" {
		t.Errorf("AAPL allocation = %+v", a)
	}
	requireDecimal(t, "AAPL quantity", a.Quantity, "5")
	requireOptional(t, "AAPL market value", a.MarketValue, "750")
	requireOptional(t, "AAPL weight", a.WeightPercent, "93.75")
	requireDecimal(t, "MSFT quantity", m.Quantity, "1")
	requireOptional(t, "MSFT market value", m.MarketValue, "50")
	requireOptional(t, "MSFT weight", m.WeightPercent, "6.25")
}

func TestSummarizeAccountWithAMissingPriceHasNoTotals(t *testing.T) {
	// Long holds AAPL (priced 110, cost 100) and is complete; Short holds MSFT (no price, cost 30) and
	// NVDA (priced 60, cost 50) and is partial. The account is partial: no total, no share of a total.
	aapl, msft, nvda := mustNewID(t), mustNewID(t), mustNewID(t)
	batch := batchOf(quoteOf(t, "AAPL", "110"), quoteOf(t, "NVDA", "60"))
	batch.Unavailable = []Symbol{symbolOf(t, "MSFT")}
	long, short := portfolioNamed(t, "Long term"), portfolioNamed(t, "Short term")

	got := SummarizeAccount(valuedAt, []ValuedPortfolio{
		valued(t, long, []Transaction{tradeIn(t, aapl, "AAPL", SideBuy, "1", "100", "0")}, nil, batch, false),
		valued(t, short, []Transaction{
			tradeIn(t, msft, "MSFT", SideBuy, "1", "30", "0"),
			tradeIn(t, nvda, "NVDA", SideBuy, "1", "50", "0"),
		}, nil, batch, false),
	})

	if got.Coverage != CoveragePartial || got.MarketValue != nil || got.UnrealizedPnL != nil || got.UnrealizedPnLPercent != nil {
		t.Fatalf("coverage = %q, totals = %v / %v / %v; want partial with every total nil", got.Coverage, got.MarketValue, got.UnrealizedPnL, got.UnrealizedPnLPercent)
	}
	requireDecimal(t, "cost basis", got.CostBasis, "180")
	requireDecimal(t, "priced market value", got.PricedMarketValue, "170")
	if len(got.MissingSymbols) != 1 || got.MissingSymbols[0].String() != "MSFT" {
		t.Errorf("MissingSymbols = %v, want [MSFT]", got.MissingSymbols)
	}
	if got.Portfolios[0].Valuation.Coverage != CoverageComplete || got.Portfolios[1].Valuation.Coverage != CoveragePartial {
		t.Errorf("portfolio coverages = %q, %q; each portfolio keeps its own", got.Portfolios[0].Valuation.Coverage, got.Portfolios[1].Valuation.Coverage)
	}
	for _, p := range got.Portfolios {
		requireNoPercent(t, "weight of "+p.Portfolio.Name.String(), p.WeightPercent)
	}
	// Priced rows first, largest first; a row without a price is last and is never worth "0".
	if allocationSymbols(got.Allocation) != "AAPL,NVDA,MSFT" {
		t.Fatalf("allocation = %s, want AAPL, NVDA, then the unpriced MSFT", allocationSymbols(got.Allocation))
	}
	requireOptional(t, "AAPL market value", got.Allocation[0].MarketValue, "110")
	unpriced := got.Allocation[2]
	if unpriced.MarketValue != nil || unpriced.Quote != nil || unpriced.Reason != ReasonNoPrice {
		t.Errorf("MSFT allocation = %+v, want no value, no quote and reason no_price", unpriced)
	}
	for _, row := range got.Allocation {
		requireNoPercent(t, "weight of "+row.Symbol.String(), row.WeightPercent)
	}
}

func TestSummarizeAccountWhenTheProviderFailedIsUnavailable(t *testing.T) {
	aapl := mustNewID(t)
	got := SummarizeAccount(valuedAt, []ValuedPortfolio{
		valued(t, portfolioNamed(t, "A"), []Transaction{
			tradeIn(t, aapl, "AAPL", SideBuy, "2", "100", "0"),
			tradeIn(t, aapl, "AAPL", SideSell, "1", "130", "0"),
		}, nil, QuoteBatch{}, true),
		valued(t, portfolioNamed(t, "B"), []Transaction{tradeIn(t, aapl, "AAPL", SideBuy, "1", "90", "0")}, nil, QuoteBatch{}, true),
	})

	if got.Coverage != CoverageUnavailable || got.MarketValue != nil || got.UnrealizedPnL != nil {
		t.Fatalf("coverage = %q, totals = %v / %v; want unavailable with nil totals", got.Coverage, got.MarketValue, got.UnrealizedPnL)
	}
	requireDecimal(t, "realized", got.RealizedPnL, "30") // does not depend on prices
	requireDecimal(t, "cost basis", got.CostBasis, "190")
	requireDecimal(t, "priced market value", got.PricedMarketValue, "0")
	if len(got.MissingSymbols) != 1 || got.MissingSymbols[0].String() != "AAPL" {
		t.Errorf("MissingSymbols = %v, want AAPL once although two portfolios hold it", got.MissingSymbols)
	}
	if len(got.Allocation) != 1 || got.Allocation[0].Reason != ReasonProviderUnavailable || got.Allocation[0].MarketValue != nil {
		t.Fatalf("allocation = %+v, want one unpriced AAPL row", got.Allocation)
	}
	requireDecimal(t, "AAPL quantity", got.Allocation[0].Quantity, "2")
}

func TestSummarizeAccountLeavesSoldOutStocksOutOfTheAllocation(t *testing.T) {
	// AAPL was sold out in A (realized 8.5) and never held in B; only MSFT is still held.
	aapl, msft := mustNewID(t), mustNewID(t)
	batch := batchOf(quoteOf(t, "MSFT", "33"))
	a, b := portfolioNamed(t, "A"), portfolioNamed(t, "B")

	got := SummarizeAccount(valuedAt, []ValuedPortfolio{
		valued(t, a, []Transaction{
			tradeIn(t, aapl, "AAPL", SideBuy, "1", "100", "0"),
			tradeIn(t, aapl, "AAPL", SideSell, "1", "108.5", "0"),
		}, nil, batch, false),
		valued(t, b, []Transaction{tradeIn(t, msft, "MSFT", SideBuy, "2", "30", "0")}, nil, batch, false),
	})

	if got.Coverage != CoverageComplete || allocationSymbols(got.Allocation) != "MSFT" {
		t.Fatalf("coverage = %q, allocation = %s; want complete with only MSFT", got.Coverage, allocationSymbols(got.Allocation))
	}
	requireDecimal(t, "realized", got.RealizedPnL, "8.5")
	requireOptional(t, "market value", got.MarketValue, "66")
	requireOptional(t, "weight of the closed portfolio", got.Portfolios[0].WeightPercent, "0")
	requireOptional(t, "weight of the open portfolio", got.Portfolios[1].WeightPercent, "100")
	requireOptional(t, "MSFT weight", got.Allocation[0].WeightPercent, "100")
}

func TestSummarizeAccountWithNothingHeldIsCompleteWithZeros(t *testing.T) {
	for name, portfolios := range map[string][]ValuedPortfolio{
		"no portfolio":       nil,
		"an empty portfolio": {valued(t, portfolioNamed(t, "Empty"), nil, nil, QuoteBatch{}, false)},
	} {
		t.Run(name, func(t *testing.T) {
			got := SummarizeAccount(valuedAt, portfolios)
			if got.Coverage != CoverageComplete {
				t.Fatalf("coverage = %q, want complete", got.Coverage)
			}
			requireOptional(t, "market value", got.MarketValue, "0")
			requireOptional(t, "unrealized", got.UnrealizedPnL, "0")
			requireNoPercent(t, "unrealized percent", got.UnrealizedPnLPercent)
			requireDecimal(t, "cost basis", got.CostBasis, "0")
			requireDecimal(t, "realized", got.RealizedPnL, "0")
			if got.Portfolios == nil || got.Allocation == nil || got.MissingSymbols == nil ||
				len(got.Portfolios) != len(portfolios) || len(got.Allocation) != 0 {
				t.Errorf("lists = %#v / %#v / %#v, want non-nil and empty but for the portfolios given", got.Portfolios, got.Allocation, got.MissingSymbols)
			}
			for _, p := range got.Portfolios {
				requireNoPercent(t, "portfolio weight", p.WeightPercent) // a share of zero does not exist
			}
		})
	}
}

func TestSummarizeAccountBreaksAllocationTiesBySymbol(t *testing.T) {
	a, m, n := mustNewID(t), mustNewID(t), mustNewID(t)
	batch := batchOf(quoteOf(t, "AAPL", "10"), quoteOf(t, "MSFT", "10"), quoteOf(t, "NVDA", "30"))
	got := SummarizeAccount(valuedAt, []ValuedPortfolio{valued(t, portfolioNamed(t, "A"), []Transaction{
		tradeIn(t, m, "MSFT", SideBuy, "1", "10", "0"),
		tradeIn(t, a, "AAPL", SideBuy, "1", "10", "0"),
		tradeIn(t, n, "NVDA", SideBuy, "1", "10", "0"),
	}, nil, batch, false)})
	if allocationSymbols(got.Allocation) != "NVDA,AAPL,MSFT" {
		t.Errorf("allocation = %s, want NVDA (largest), then AAPL before MSFT at the same value", allocationSymbols(got.Allocation))
	}
}
