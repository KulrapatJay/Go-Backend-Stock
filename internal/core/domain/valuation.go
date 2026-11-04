package domain

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// Holding is what a portfolio holds of one instrument, replayed from its transactions. A position that
// was sold out stays (Quantity 0) because it carries realized profit and loss.
type Holding struct {
	InstrumentID   ID
	Symbol         Symbol // the instrument's latest symbol in the history
	InstrumentName string
	Quantity       decimal.Decimal
	CostBasis      decimal.Decimal
	AverageCost    decimal.Decimal
	RealizedPnL    decimal.Decimal
}

// BuildHoldings turns replayed positions into holdings sorted by symbol. The symbol is the one on the
// instrument's latest transaction in history; names holds instrument names, and an instrument without
// one is named by its symbol.
func BuildHoldings(positions map[ID]Position, history []Transaction, names map[ID]string) []Holding {
	symbols := make(map[ID]Symbol, len(positions))
	for _, t := range history {
		symbols[t.InstrumentID] = t.Symbol
	}
	holdings := make([]Holding, 0, len(positions))
	for id, p := range positions {
		symbol := symbols[id]
		name := names[id]
		if name == "" {
			name = symbol.String()
		}
		holdings = append(holdings, Holding{
			InstrumentID: id, Symbol: symbol, InstrumentName: name,
			Quantity: p.Quantity, CostBasis: p.CostBasis, AverageCost: p.AverageCost, RealizedPnL: p.RealizedPnL,
		})
	}
	sort.Slice(holdings, func(i, j int) bool { return holdings[i].Symbol.String() < holdings[j].Symbol.String() })
	return holdings
}

// OpenSymbols lists, in the order of holdings, the symbols still held (quantity above zero). It is what
// a price provider is asked about; the result is never nil.
func OpenSymbols(holdings []Holding) []Symbol {
	symbols := []Symbol{}
	for _, h := range holdings {
		if h.Quantity.IsPositive() {
			symbols = append(symbols, h.Symbol)
		}
	}
	return symbols
}

// UnavailableReason says why a position that is still held has no market value.
type UnavailableReason string

const (
	// ReasonNoPrice means the provider answered but has no price for the symbol.
	ReasonNoPrice UnavailableReason = "no_price"
	// ReasonProviderUnavailable means the provider could not be asked or did not answer in time.
	ReasonProviderUnavailable UnavailableReason = "provider_unavailable"
)

// Coverage says how many of the positions still held have a price.
type Coverage string

const (
	CoverageComplete    Coverage = "complete"
	CoveragePartial     Coverage = "partial"
	CoverageUnavailable Coverage = "unavailable"
)

// PositionValuation is a Holding with its price. MarketValue and UnrealizedPnL are nil when a position
// that is still held has no price: a missing price is never valued as zero. A closed position has
// neither a quote nor a reason, and its values are zero.
type PositionValuation struct {
	Holding       Holding
	Quote         *PriceQuote
	MarketValue   *decimal.Decimal
	UnrealizedPnL *decimal.Decimal
	Reason        UnavailableReason
}

// PortfolioValuation is the result of valuing a portfolio at CalculatedAt. MarketValue and
// UnrealizedPnL are nil unless every position still held has a price; PricedMarketValue is then only
// the subtotal of the priced positions and is never presented as the whole. RealizedPnL covers every
// position, closed or open, and does not depend on prices.
type PortfolioValuation struct {
	PortfolioID       ID
	CalculatedAt      time.Time
	Coverage          Coverage
	MarketValue       *decimal.Decimal
	UnrealizedPnL     *decimal.Decimal
	RealizedPnL       decimal.Decimal
	PricedMarketValue decimal.Decimal
	MissingSymbols    []Symbol
	Positions         []PositionValuation
}

// ValuePortfolio prices holdings with batch. failed means the provider could not be asked at all: every
// position still held then has no price because of ReasonProviderUnavailable and batch is ignored.
// market value = quantity x price and unrealized = market value - remaining cost, both exact; nothing
// is rounded and no future selling fee is assumed.
func ValuePortfolio(portfolioID ID, at time.Time, holdings []Holding, batch QuoteBatch, failed bool) PortfolioValuation {
	v := PortfolioValuation{
		PortfolioID: portfolioID, CalculatedAt: at.UTC(),
		MissingSymbols: []Symbol{}, Positions: make([]PositionValuation, 0, len(holdings)),
	}
	var priced, unrealized decimal.Decimal
	open, pricedCount := 0, 0
	for _, h := range holdings {
		v.RealizedPnL = v.RealizedPnL.Add(h.RealizedPnL)
		position := PositionValuation{Holding: h}
		if !h.Quantity.IsPositive() {
			zeroValue, zeroPnL := decimal.Zero, decimal.Zero
			position.MarketValue, position.UnrealizedPnL = &zeroValue, &zeroPnL
			v.Positions = append(v.Positions, position)
			continue
		}
		open++
		quote, ok := batch.Quotes[h.Symbol.String()]
		switch {
		case failed:
			position.Reason = ReasonProviderUnavailable
		case !ok:
			position.Reason = ReasonNoPrice
		default:
			value := h.Quantity.Mul(quote.Price.Decimal())
			pnl := value.Sub(h.CostBasis)
			position.Quote, position.MarketValue, position.UnrealizedPnL = &quote, &value, &pnl
			priced = priced.Add(value)
			unrealized = unrealized.Add(pnl)
			pricedCount++
		}
		if position.Reason != "" {
			v.MissingSymbols = append(v.MissingSymbols, h.Symbol)
		}
		v.Positions = append(v.Positions, position)
	}
	v.PricedMarketValue = priced
	switch pricedCount {
	case open:
		v.Coverage = CoverageComplete
		v.MarketValue, v.UnrealizedPnL = &priced, &unrealized
	case 0:
		v.Coverage = CoverageUnavailable
	default:
		v.Coverage = CoveragePartial
	}
	return v
}
