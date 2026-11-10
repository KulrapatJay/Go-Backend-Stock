package domain

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// ValuedPortfolio is one portfolio of a user with its valuation.
type ValuedPortfolio struct {
	Portfolio Portfolio
	Valuation PortfolioValuation
}

// AccountPortfolio is a portfolio in an AccountValuation. WeightPercent is its market value / the
// market value of the account x 100; it is nil unless the account is complete and worth more than
// zero, and a portfolio that holds nothing then weighs 0.
type AccountPortfolio struct {
	Portfolio     Portfolio
	Valuation     PortfolioValuation
	WeightPercent *decimal.Decimal
}

// Allocation is what a user still holds of one instrument over all portfolios. MarketValue is nil when
// the instrument has no price in any portfolio that holds it (see Reason); it is never zero for a
// missing price. WeightPercent is MarketValue / the market value of the account x 100 and is nil
// unless the account is complete.
type Allocation struct {
	InstrumentID   ID
	Symbol         Symbol
	InstrumentName string
	Quantity       decimal.Decimal
	Quote          *PriceQuote
	MarketValue    *decimal.Decimal
	WeightPercent  *decimal.Decimal
	Reason         UnavailableReason
}

// AccountValuation adds up every portfolio of one user at CalculatedAt. It follows the rules of
// PortfolioValuation: MarketValue, UnrealizedPnL and UnrealizedPnLPercent are nil unless every position
// still held in every portfolio has a price, PricedMarketValue is then only the subtotal of the priced
// positions, and CostBasis and RealizedPnL never depend on prices. Portfolios keeps the order given.
// Allocation has one row per instrument still held, the largest market value first; rows without a
// price come last, and ties are in symbol order.
type AccountValuation struct {
	CalculatedAt         time.Time
	Coverage             Coverage
	CostBasis            decimal.Decimal
	MarketValue          *decimal.Decimal
	UnrealizedPnL        *decimal.Decimal
	UnrealizedPnLPercent *decimal.Decimal
	RealizedPnL          decimal.Decimal
	PricedMarketValue    decimal.Decimal
	MissingSymbols       []Symbol
	Portfolios           []AccountPortfolio
	Allocation           []Allocation
}

// SummarizeAccount adds up portfolios that were each valued at the same moment with the same prices.
// It only adds and compares what the valuations hold; no price is looked at again.
func SummarizeAccount(at time.Time, portfolios []ValuedPortfolio) AccountValuation {
	a := AccountValuation{
		CalculatedAt: at.UTC(), MissingSymbols: []Symbol{},
		Portfolios: make([]AccountPortfolio, 0, len(portfolios)), Allocation: []Allocation{},
	}
	var unrealized decimal.Decimal
	open, priced := 0, 0
	rows := map[ID]*Allocation{}
	var order []ID
	missing := map[string]Symbol{}

	for _, p := range portfolios {
		v := p.Valuation
		a.Portfolios = append(a.Portfolios, AccountPortfolio{Portfolio: p.Portfolio, Valuation: v})
		a.CostBasis = a.CostBasis.Add(v.CostBasis)
		a.RealizedPnL = a.RealizedPnL.Add(v.RealizedPnL)
		a.PricedMarketValue = a.PricedMarketValue.Add(v.PricedMarketValue)
		for _, s := range v.MissingSymbols {
			missing[s.String()] = s
		}
		for _, position := range v.Positions {
			h := position.Holding
			if !h.Quantity.IsPositive() {
				continue
			}
			open++
			row, seen := rows[h.InstrumentID]
			if !seen {
				zero := decimal.Zero
				row = &Allocation{InstrumentID: h.InstrumentID, Symbol: h.Symbol, InstrumentName: h.InstrumentName, MarketValue: &zero}
				rows[h.InstrumentID] = row
				order = append(order, h.InstrumentID)
			}
			row.Quantity = row.Quantity.Add(h.Quantity)
			if position.MarketValue == nil {
				// One unpriced position leaves the whole row without a value: a part is not the whole.
				row.MarketValue, row.Quote = nil, nil
				if row.Reason == "" {
					row.Reason = position.Reason
				}
				continue
			}
			priced++
			unrealized = unrealized.Add(*position.UnrealizedPnL)
			if row.Reason == "" {
				value := row.MarketValue.Add(*position.MarketValue)
				row.MarketValue, row.Quote = &value, position.Quote
			}
		}
	}

	for _, s := range missing {
		a.MissingSymbols = append(a.MissingSymbols, s)
	}
	sort.Slice(a.MissingSymbols, func(i, j int) bool { return a.MissingSymbols[i].String() < a.MissingSymbols[j].String() })

	switch priced {
	case open:
		a.Coverage = CoverageComplete
		total := a.PricedMarketValue
		a.MarketValue, a.UnrealizedPnL = &total, &unrealized
		a.UnrealizedPnLPercent = percentOf(unrealized, a.CostBasis)
		for i := range a.Portfolios {
			a.Portfolios[i].WeightPercent = percentOf(*a.Portfolios[i].Valuation.MarketValue, total)
		}
		for _, row := range rows {
			row.WeightPercent = percentOf(*row.MarketValue, total)
		}
	case 0:
		a.Coverage = CoverageUnavailable
	default:
		a.Coverage = CoveragePartial
	}

	for _, id := range order {
		a.Allocation = append(a.Allocation, *rows[id])
	}
	sort.SliceStable(a.Allocation, func(i, j int) bool {
		x, y := a.Allocation[i], a.Allocation[j]
		switch {
		case (x.MarketValue == nil) != (y.MarketValue == nil):
			return x.MarketValue != nil
		case x.MarketValue != nil && !x.MarketValue.Equal(*y.MarketValue):
			return x.MarketValue.GreaterThan(*y.MarketValue)
		default:
			return x.Symbol.String() < y.Symbol.String()
		}
	})
	return a
}
