package http

import (
	"time"

	"github.com/shopspring/decimal"

	"go-backend-stock/internal/core/domain"
)

// HoldingResponse is what a portfolio holds of one stock, calculated from its transactions. Every
// number is a decimal string without trailing zeros and with the precision of the calculation (an
// average cost can have 28 decimals); rounding for display is up to the client. A stock that was sold
// out stays in the list with quantity "0" because it carries realized profit and loss.
type HoldingResponse struct {
	Symbol         string `json:"symbol" example:"AAPL" validate:"required"`
	InstrumentName string `json:"instrument_name" example:"Apple Inc." validate:"required"`
	Quantity       string `json:"quantity" example:"1.5" validate:"required"`
	CostBasis      string `json:"cost_basis" example:"151.5" validate:"required"`
	AverageCost    string `json:"average_cost" example:"101" validate:"required"`
	RealizedPnL    string `json:"realized_pnl" example:"8.5" validate:"required"`
}

// HoldingsResponse is the list of holdings of a portfolio, sorted by symbol.
type HoldingsResponse struct {
	Data []HoldingResponse `json:"data" validate:"required"`
}

// QuoteResponse is the reference price of a stock. price_type says what kind of price it is and
// delay_minutes how old a price of that kind is at least, so a client labels the price from the data
// instead of hard-coding a text. Today every price is a delayed_bar_close with delay_minutes 15: the
// close of the last bar before the cutoff (15 minutes before the valuation), not a bid or ask price and
// not a price one could trade at. More kinds may be added (a latest trade with delay_minutes 0, for
// one); show a kind you do not know as a plain price with its as_of. as_of is the time of the source
// data and says how old this very price is, fetched_at the time the server got it. is_demo marks
// sample data.
type QuoteResponse struct {
	Price         string    `json:"price" example:"110" validate:"required"`
	Currency      string    `json:"currency" enums:"USD" example:"USD" validate:"required"`
	PriceType     string    `json:"price_type" enums:"delayed_bar_close" example:"delayed_bar_close" validate:"required"`
	DelayMinutes  int       `json:"delay_minutes" example:"15" minimum:"0" validate:"required"`
	AsOf          time.Time `json:"as_of" format:"date-time" example:"2026-11-04T14:30:00Z" validate:"required"`
	FetchedAt     time.Time `json:"fetched_at" format:"date-time" example:"2026-11-04T14:46:00Z" validate:"required"`
	Source        string    `json:"source" example:"demo" validate:"required"`
	Feed          string    `json:"feed" example:"demo" validate:"required"`
	IsDemo        bool      `json:"is_demo" example:"true" validate:"required"`
	Stale         bool      `json:"stale" example:"false" validate:"required"`
	Cached        bool      `json:"cached" example:"false" validate:"required"`
	SessionStatus string    `json:"session_status" enums:"open,market_closed,unknown" example:"unknown" validate:"required"`
}

// PositionResponse is a holding with its price. quote, market_value and unrealized_pnl are null when a
// stock that is still held has no price (see unavailable_reason); null never means zero. A stock that
// was sold out has no quote, no reason and market_value and unrealized_pnl of "0".
//
// The two percentages are in percent, not ratios ("8.5" is 8.5%), with the precision of the calculation
// (up to 28 decimals); round them for display only. unrealized_pnl_percent = unrealized_pnl / cost_basis
// x 100 and is null when unrealized_pnl is null and for a stock that was sold out (its cost_basis is
// "0"). weight_percent = market_value / market_value of the portfolio x 100 and is null unless coverage
// is complete and the portfolio is worth more than zero; a stock that was sold out then weighs "0".
// null never means 0%: "0" is a stock at break even, or one that takes no part of the portfolio.
type PositionResponse struct {
	HoldingResponse
	Quote                *QuoteResponse `json:"quote" extensions:"x-nullable" validate:"required"`
	MarketValue          *string        `json:"market_value" extensions:"x-nullable" example:"165" validate:"required"`
	UnrealizedPnL        *string        `json:"unrealized_pnl" extensions:"x-nullable" example:"13.5" validate:"required"`
	UnrealizedPnLPercent *string        `json:"unrealized_pnl_percent" extensions:"x-nullable" example:"8.9108910891089108910891089109" validate:"required"`
	WeightPercent        *string        `json:"weight_percent" extensions:"x-nullable" example:"100" validate:"required"`
	UnavailableReason    *string        `json:"unavailable_reason" extensions:"x-nullable" enums:"no_price,provider_unavailable" example:"no_price" validate:"required"`
}

// ValuationResponse is what a portfolio is worth, in USD, at calculated_at. coverage is complete when
// every stock still held has a price, partial when only some do, and unavailable when none does (or the
// price provider could not be reached). Unless coverage is complete, market_value, unrealized_pnl and
// unrealized_pnl_percent are null and priced_market_value is only the subtotal of the priced stocks.
// realized_pnl and cost_basis (the remaining cost of the stocks still held) never depend on prices.
// unrealized_pnl_percent = unrealized_pnl / cost_basis x 100, in percent ("8.5" is 8.5%); it is also
// null when nothing is held (cost_basis "0"), never "0". calculated_at is when the transactions were
// read, not when the prices were taken.
type ValuationResponse struct {
	PortfolioID          string             `json:"portfolio_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d" validate:"required"`
	Currency             string             `json:"currency" enums:"USD" example:"USD" validate:"required"`
	CalculatedAt         time.Time          `json:"calculated_at" format:"date-time" example:"2026-11-04T15:00:00Z" validate:"required"`
	Coverage             string             `json:"coverage" enums:"complete,partial,unavailable" example:"complete" validate:"required"`
	CostBasis            string             `json:"cost_basis" example:"151.5" validate:"required"`
	MarketValue          *string            `json:"market_value" extensions:"x-nullable" example:"165" validate:"required"`
	UnrealizedPnL        *string            `json:"unrealized_pnl" extensions:"x-nullable" example:"13.5" validate:"required"`
	UnrealizedPnLPercent *string            `json:"unrealized_pnl_percent" extensions:"x-nullable" example:"8.9108910891089108910891089109" validate:"required"`
	RealizedPnL          string             `json:"realized_pnl" example:"8.5" validate:"required"`
	PricedMarketValue    string             `json:"priced_market_value" example:"165" validate:"required"`
	MissingSymbols       []string           `json:"missing_symbols" example:"MSFT,NVDA" validate:"required"`
	Positions            []PositionResponse `json:"positions" validate:"required"`
}

func newHoldingResponse(h domain.Holding) HoldingResponse {
	return HoldingResponse{
		Symbol: h.Symbol.String(), InstrumentName: h.InstrumentName, Quantity: h.Quantity.String(),
		CostBasis: h.CostBasis.String(), AverageCost: h.AverageCost.String(), RealizedPnL: h.RealizedPnL.String(),
	}
}

func newHoldingsResponse(holdings []domain.Holding) HoldingsResponse {
	out := HoldingsResponse{Data: make([]HoldingResponse, 0, len(holdings))}
	for _, h := range holdings {
		out.Data = append(out.Data, newHoldingResponse(h))
	}
	return out
}

func newQuoteResponse(q domain.PriceQuote) *QuoteResponse {
	return &QuoteResponse{
		Price: q.Price.String(), Currency: q.Currency,
		PriceType: string(q.PriceType), DelayMinutes: int(q.PriceType.MinimumDelay() / time.Minute),
		AsOf: q.AsOf.UTC(), FetchedAt: q.FetchedAt.UTC(),
		Source: q.Source, Feed: q.Feed, IsDemo: q.IsDemo, Stale: q.Stale, Cached: q.Cached, SessionStatus: string(q.SessionStatus),
	}
}

func optionalDecimal(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}

func newValuationResponse(v domain.PortfolioValuation) ValuationResponse {
	out := ValuationResponse{
		PortfolioID: v.PortfolioID.String(), Currency: "USD", CalculatedAt: v.CalculatedAt.UTC(), Coverage: string(v.Coverage),
		CostBasis:   v.CostBasis.String(),
		MarketValue: optionalDecimal(v.MarketValue), UnrealizedPnL: optionalDecimal(v.UnrealizedPnL),
		UnrealizedPnLPercent: optionalDecimal(v.UnrealizedPnLPercent),
		RealizedPnL:          v.RealizedPnL.String(), PricedMarketValue: v.PricedMarketValue.String(),
		MissingSymbols: make([]string, 0, len(v.MissingSymbols)), Positions: make([]PositionResponse, 0, len(v.Positions)),
	}
	for _, s := range v.MissingSymbols {
		out.MissingSymbols = append(out.MissingSymbols, s.String())
	}
	for _, p := range v.Positions {
		position := PositionResponse{
			HoldingResponse: newHoldingResponse(p.Holding),
			MarketValue:     optionalDecimal(p.MarketValue), UnrealizedPnL: optionalDecimal(p.UnrealizedPnL),
			UnrealizedPnLPercent: optionalDecimal(p.UnrealizedPnLPercent), WeightPercent: optionalDecimal(p.WeightPercent),
		}
		if p.Quote != nil {
			position.Quote = newQuoteResponse(*p.Quote)
		}
		position.UnavailableReason = optionalReason(p.Reason)
		out.Positions = append(out.Positions, position)
	}
	return out
}

// AccountPortfolioResponse is one portfolio in the account summary: the totals of its own valuation,
// with the same rules (see ValuationResponse), without its positions. weight_percent = market_value /
// market_value of the account x 100, in percent; it is null unless the coverage of the account is
// complete and the account is worth more than zero. A portfolio that holds nothing then weighs "0".
type AccountPortfolioResponse struct {
	PortfolioID          string  `json:"portfolio_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d" validate:"required"`
	Name                 string  `json:"name" example:"Long term" validate:"required"`
	Coverage             string  `json:"coverage" enums:"complete,partial,unavailable" example:"complete" validate:"required"`
	CostBasis            string  `json:"cost_basis" example:"200" validate:"required"`
	MarketValue          *string `json:"market_value" extensions:"x-nullable" example:"300" validate:"required"`
	UnrealizedPnL        *string `json:"unrealized_pnl" extensions:"x-nullable" example:"100" validate:"required"`
	UnrealizedPnLPercent *string `json:"unrealized_pnl_percent" extensions:"x-nullable" example:"50" validate:"required"`
	RealizedPnL          string  `json:"realized_pnl" example:"2" validate:"required"`
	WeightPercent        *string `json:"weight_percent" extensions:"x-nullable" example:"37.5" validate:"required"`
}

// AllocationResponse is what the user still holds of one stock over all portfolios; a stock that was
// sold out everywhere is not listed. quote is the one price every portfolio was valued with.
// market_value is null when the stock has no price (see unavailable_reason), never "0". weight_percent
// = market_value / market_value of the account x 100, in percent, and is null unless the coverage of
// the account is complete.
type AllocationResponse struct {
	Symbol            string         `json:"symbol" example:"AAPL" validate:"required"`
	InstrumentName    string         `json:"instrument_name" example:"Apple Inc." validate:"required"`
	Quantity          string         `json:"quantity" example:"5" validate:"required"`
	Quote             *QuoteResponse `json:"quote" extensions:"x-nullable" validate:"required"`
	MarketValue       *string        `json:"market_value" extensions:"x-nullable" example:"750" validate:"required"`
	WeightPercent     *string        `json:"weight_percent" extensions:"x-nullable" example:"93.75" validate:"required"`
	UnavailableReason *string        `json:"unavailable_reason" extensions:"x-nullable" enums:"no_price,provider_unavailable" example:"no_price" validate:"required"`
}

// AccountValuationResponse adds up every portfolio of the user, in USD, at calculated_at. The rules are
// those of ValuationResponse, over all portfolios: coverage is complete when every stock still held in
// any portfolio has a price; unless it is, market_value, unrealized_pnl and unrealized_pnl_percent are
// null and priced_market_value is only the subtotal of the priced stocks. cost_basis and realized_pnl
// never depend on prices. Percentages are in percent ("8.5" is 8.5%), not rounded. portfolios lists
// every portfolio, newest first as GET /api/v1/portfolios does, and is not paged: the totals count all
// of them. allocation has one row per stock still held, the same stock in several portfolios merged,
// the largest market_value first and rows without a price last. A user without portfolios or trades
// gets zeros and empty lists.
type AccountValuationResponse struct {
	Currency             string                     `json:"currency" enums:"USD" example:"USD" validate:"required"`
	CalculatedAt         time.Time                  `json:"calculated_at" format:"date-time" example:"2026-11-10T15:00:00Z" validate:"required"`
	Coverage             string                     `json:"coverage" enums:"complete,partial,unavailable" example:"complete" validate:"required"`
	CostBasis            string                     `json:"cost_basis" example:"610" validate:"required"`
	MarketValue          *string                    `json:"market_value" extensions:"x-nullable" example:"800" validate:"required"`
	PricedMarketValue    string                     `json:"priced_market_value" example:"800" validate:"required"`
	UnrealizedPnL        *string                    `json:"unrealized_pnl" extensions:"x-nullable" example:"190" validate:"required"`
	UnrealizedPnLPercent *string                    `json:"unrealized_pnl_percent" extensions:"x-nullable" example:"31.1475409836065573770491803279" validate:"required"`
	RealizedPnL          string                     `json:"realized_pnl" example:"2" validate:"required"`
	MissingSymbols       []string                   `json:"missing_symbols" example:"MSFT,NVDA" validate:"required"`
	Portfolios           []AccountPortfolioResponse `json:"portfolios" validate:"required"`
	Allocation           []AllocationResponse       `json:"allocation" validate:"required"`
}

func optionalReason(r domain.UnavailableReason) *string {
	if r == "" {
		return nil
	}
	reason := string(r)
	return &reason
}

func newAccountValuationResponse(a domain.AccountValuation) AccountValuationResponse {
	out := AccountValuationResponse{
		Currency: "USD", CalculatedAt: a.CalculatedAt.UTC(), Coverage: string(a.Coverage), CostBasis: a.CostBasis.String(),
		MarketValue: optionalDecimal(a.MarketValue), PricedMarketValue: a.PricedMarketValue.String(),
		UnrealizedPnL: optionalDecimal(a.UnrealizedPnL), UnrealizedPnLPercent: optionalDecimal(a.UnrealizedPnLPercent),
		RealizedPnL:    a.RealizedPnL.String(),
		MissingSymbols: make([]string, 0, len(a.MissingSymbols)),
		Portfolios:     make([]AccountPortfolioResponse, 0, len(a.Portfolios)),
		Allocation:     make([]AllocationResponse, 0, len(a.Allocation)),
	}
	for _, s := range a.MissingSymbols {
		out.MissingSymbols = append(out.MissingSymbols, s.String())
	}
	for _, p := range a.Portfolios {
		v := p.Valuation
		out.Portfolios = append(out.Portfolios, AccountPortfolioResponse{
			PortfolioID: p.Portfolio.ID.String(), Name: p.Portfolio.Name.String(), Coverage: string(v.Coverage),
			CostBasis: v.CostBasis.String(), MarketValue: optionalDecimal(v.MarketValue),
			UnrealizedPnL: optionalDecimal(v.UnrealizedPnL), UnrealizedPnLPercent: optionalDecimal(v.UnrealizedPnLPercent),
			RealizedPnL: v.RealizedPnL.String(), WeightPercent: optionalDecimal(p.WeightPercent),
		})
	}
	for _, row := range a.Allocation {
		item := AllocationResponse{
			Symbol: row.Symbol.String(), InstrumentName: row.InstrumentName, Quantity: row.Quantity.String(),
			MarketValue: optionalDecimal(row.MarketValue), WeightPercent: optionalDecimal(row.WeightPercent),
			UnavailableReason: optionalReason(row.Reason),
		}
		if row.Quote != nil {
			item.Quote = newQuoteResponse(*row.Quote)
		}
		out.Allocation = append(out.Allocation, item)
	}
	return out
}
