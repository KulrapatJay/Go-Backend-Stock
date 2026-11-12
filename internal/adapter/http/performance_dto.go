package http

import (
	"github.com/shopspring/decimal"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PerformanceRangeResponse is the first and last day of the points. Both are null when there is no point
// (the portfolio held nothing, or none of what it held has a price in the range).
type PerformanceRangeResponse struct {
	From *string `json:"from" extensions:"x-nullable" format:"date" example:"2025-11-12" validate:"required"`
	To   *string `json:"to" extensions:"x-nullable" format:"date" example:"2026-11-10" validate:"required"`
}

// PerformancePointResponse is the portfolio on one day: what the shares held at the end of the day are
// worth, and the time-weighted return since range.from in percent. Both are null together on a day a stock
// the portfolio still held has no bar: the value of that day is unknown, never 0.
type PerformancePointResponse struct {
	Date          string  `json:"date" format:"date" example:"2025-11-13" validate:"required"`
	MarketValue   *string `json:"market_value" extensions:"x-nullable" example:"1012.5" validate:"required"`
	ReturnPercent *string `json:"return_percent" extensions:"x-nullable" example:"1.25" validate:"required"`
}

// PortfolioPerformanceResponse is the line of the portfolio. return_percent is that of the last point that
// has one, null when there is none.
type PortfolioPerformanceResponse struct {
	ReturnPercent *string                    `json:"return_percent" extensions:"x-nullable" example:"18.4" validate:"required"`
	Points        []PerformancePointResponse `json:"points" validate:"required"`
}

// BenchmarkPointResponse is the return of a benchmark on a day of the portfolio, since range.from. It is null
// when the benchmark has no bar that day or none on range.from.
type BenchmarkPointResponse struct {
	Date          string  `json:"date" format:"date" example:"2025-11-13" validate:"required"`
	ReturnPercent *string `json:"return_percent" extensions:"x-nullable" example:"0.8" validate:"required"`
}

// BenchmarkPerformanceResponse is a benchmark measured on the same days as the portfolio. excess_return_percent
// is the portfolio return minus the benchmark return at the last day of the portfolio, in percentage points; it
// is null when either has no value.
type BenchmarkPerformanceResponse struct {
	Symbol              string                   `json:"symbol" enums:"SPY,QQQ" example:"SPY" validate:"required"`
	Label               string                   `json:"label" example:"S&P 500 (SPY)" validate:"required"`
	ReturnPercent       *string                  `json:"return_percent" extensions:"x-nullable" example:"12.1" validate:"required"`
	ExcessReturnPercent *string                  `json:"excess_return_percent" extensions:"x-nullable" example:"6.3" validate:"required"`
	Points              []BenchmarkPointResponse `json:"points" validate:"required"`
}

// PerformanceResponse is the daily value and the time-weighted return of a portfolio or an account, with
// optional benchmarks on the same days. method says how the return is calculated (time_weighted) and
// fees_included that trade fees are part of it. adjustment is raw: prices are not changed for stock splits,
// and dividends are not included, for the portfolio and the benchmarks alike. interval is always 1d today; a
// client must not fail on another value. coverage says whether every day of the line has a value (complete),
// some do not (partial) or none does (unavailable). source and is_demo say where the prices come from.
// data_through is the last day every price needed is stored for; it is before range.to when the newest days
// could not be fetched (null when no price was needed).
type PerformanceResponse struct {
	Currency     string                         `json:"currency" enums:"USD" example:"USD" validate:"required"`
	Range        PerformanceRangeResponse       `json:"range" validate:"required"`
	Method       string                         `json:"method" enums:"time_weighted" example:"time_weighted" validate:"required"`
	FeesIncluded bool                           `json:"fees_included" example:"true" validate:"required"`
	Adjustment   string                         `json:"adjustment" enums:"raw" example:"raw" validate:"required"`
	Interval     string                         `json:"interval" enums:"1d" example:"1d" validate:"required"`
	Coverage     string                         `json:"coverage" enums:"complete,partial,unavailable" example:"complete" validate:"required"`
	Source       string                         `json:"source" example:"alpaca" validate:"required"`
	IsDemo       bool                           `json:"is_demo" example:"false" validate:"required"`
	DataThrough  *string                        `json:"data_through" extensions:"x-nullable" format:"date" example:"2026-11-10" validate:"required"`
	Portfolio    PortfolioPerformanceResponse   `json:"portfolio" validate:"required"`
	Benchmarks   []BenchmarkPerformanceResponse `json:"benchmarks" validate:"required"`
}

func newPerformanceResponse(r port.PerformanceReport) PerformanceResponse {
	out := PerformanceResponse{
		Currency: "USD", Method: "time_weighted", FeesIncluded: true,
		Adjustment: domain.BarAdjustmentRaw, Interval: domain.BarTimeframeDaily,
		Coverage: string(r.Coverage), Source: string(r.Source), IsDemo: r.Source == domain.BarSourceDemo,
		Range:       PerformanceRangeResponse{From: dateOrNil(r.From), To: dateOrNil(r.To)},
		DataThrough: dateOrNil(r.DataThrough),
		Portfolio: PortfolioPerformanceResponse{
			ReturnPercent: decimalOrNil(r.ReturnPercent),
			Points:        make([]PerformancePointResponse, 0, len(r.Points)),
		},
		Benchmarks: make([]BenchmarkPerformanceResponse, 0, len(r.Benchmarks)),
	}
	for _, p := range r.Points {
		out.Portfolio.Points = append(out.Portfolio.Points, PerformancePointResponse{
			Date: p.Date.String(), MarketValue: decimalOrNil(p.MarketValue), ReturnPercent: decimalOrNil(p.ReturnPercent),
		})
	}
	for _, b := range r.Benchmarks {
		item := BenchmarkPerformanceResponse{
			Symbol: b.Symbol.String(), Label: b.Label,
			ReturnPercent: decimalOrNil(b.ReturnPercent), ExcessReturnPercent: decimalOrNil(b.ExcessReturnPercent),
			Points: make([]BenchmarkPointResponse, 0, len(b.Points)),
		}
		for _, p := range b.Points {
			item.Points = append(item.Points, BenchmarkPointResponse{Date: p.Date.String(), ReturnPercent: decimalOrNil(p.ReturnPercent)})
		}
		out.Benchmarks = append(out.Benchmarks, item)
	}
	return out
}

func dateOrNil(d domain.TradingDate) *string {
	if d.IsZero() {
		return nil
	}
	s := d.String()
	return &s
}

func decimalOrNil(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}
