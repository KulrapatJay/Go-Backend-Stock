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

// QuoteResponse is the delayed reference price of a stock: the close of the last bar before the cutoff
// (15 minutes before the valuation), not a bid or ask price and not a price one could trade at.
// as_of is the time of the source data, fetched_at the time the server got it. is_demo marks sample data.
type QuoteResponse struct {
	Price         string    `json:"price" example:"110" validate:"required"`
	Currency      string    `json:"currency" enums:"USD" example:"USD" validate:"required"`
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
type PositionResponse struct {
	HoldingResponse
	Quote             *QuoteResponse `json:"quote" extensions:"x-nullable" validate:"required"`
	MarketValue       *string        `json:"market_value" extensions:"x-nullable" example:"165" validate:"required"`
	UnrealizedPnL     *string        `json:"unrealized_pnl" extensions:"x-nullable" example:"13.5" validate:"required"`
	UnavailableReason *string        `json:"unavailable_reason" extensions:"x-nullable" enums:"no_price,provider_unavailable" example:"no_price" validate:"required"`
}

// ValuationResponse is what a portfolio is worth, in USD, at calculated_at. coverage is complete when
// every stock still held has a price, partial when only some do, and unavailable when none does (or the
// price provider could not be reached). Unless coverage is complete, market_value and unrealized_pnl are
// null and priced_market_value is only the subtotal of the priced stocks. realized_pnl never depends on
// prices. calculated_at is when the transactions were read, not when the prices were taken.
type ValuationResponse struct {
	PortfolioID       string             `json:"portfolio_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d" validate:"required"`
	Currency          string             `json:"currency" enums:"USD" example:"USD" validate:"required"`
	CalculatedAt      time.Time          `json:"calculated_at" format:"date-time" example:"2026-11-04T15:00:00Z" validate:"required"`
	Coverage          string             `json:"coverage" enums:"complete,partial,unavailable" example:"complete" validate:"required"`
	MarketValue       *string            `json:"market_value" extensions:"x-nullable" example:"165" validate:"required"`
	UnrealizedPnL     *string            `json:"unrealized_pnl" extensions:"x-nullable" example:"13.5" validate:"required"`
	RealizedPnL       string             `json:"realized_pnl" example:"8.5" validate:"required"`
	PricedMarketValue string             `json:"priced_market_value" example:"165" validate:"required"`
	MissingSymbols    []string           `json:"missing_symbols" example:"MSFT,NVDA" validate:"required"`
	Positions         []PositionResponse `json:"positions" validate:"required"`
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
		Price: q.Price.String(), Currency: q.Currency, AsOf: q.AsOf.UTC(), FetchedAt: q.FetchedAt.UTC(),
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
		MarketValue: optionalDecimal(v.MarketValue), UnrealizedPnL: optionalDecimal(v.UnrealizedPnL),
		RealizedPnL: v.RealizedPnL.String(), PricedMarketValue: v.PricedMarketValue.String(),
		MissingSymbols: make([]string, 0, len(v.MissingSymbols)), Positions: make([]PositionResponse, 0, len(v.Positions)),
	}
	for _, s := range v.MissingSymbols {
		out.MissingSymbols = append(out.MissingSymbols, s.String())
	}
	for _, p := range v.Positions {
		position := PositionResponse{
			HoldingResponse: newHoldingResponse(p.Holding),
			MarketValue:     optionalDecimal(p.MarketValue), UnrealizedPnL: optionalDecimal(p.UnrealizedPnL),
		}
		if p.Quote != nil {
			position.Quote = newQuoteResponse(*p.Quote)
		}
		if p.Reason != "" {
			reason := string(p.Reason)
			position.UnavailableReason = &reason
		}
		out.Positions = append(out.Positions, position)
	}
	return out
}
