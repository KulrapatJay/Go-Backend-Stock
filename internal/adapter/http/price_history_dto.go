package http

import "go-backend-stock/internal/core/domain"

// DailyBarResponse is one trading day of a stock: unadjusted prices in USD and the shares traded. date is
// the market date in New York (YYYY-MM-DD), not a timestamp, so it never moves with the time zone of the
// reader. Every number is a decimal string; a client turns them into numbers only to hand them to a chart
// library and never calculates with them or sends them back.
type DailyBarResponse struct {
	Date   string `json:"date" format:"date" example:"2026-11-10" validate:"required"`
	Open   string `json:"open" example:"351.35" validate:"required"`
	High   string `json:"high" example:"353.87" validate:"required"`
	Low    string `json:"low" example:"346.4605" validate:"required"`
	Close  string `json:"close" example:"348.51" validate:"required"`
	Volume string `json:"volume" example:"29910098" validate:"required"`
}

// BarsResponse is the daily price history of one stock, oldest first. adjustment is raw: the prices of the
// day as they were traded, not changed for later stock splits or dividends, so the chart of a stock that
// split falls at the split. A day without a bar (a weekend, a market holiday, a day before the stock was
// listed or before the history starts, 2016-01-04) is left out, never filled with 0. Only days that have
// closed in New York are included. source and feed say where the bars come from and is_demo marks sample
// data. data_from and data_through are the first and last date the server has checked with the provider
// for this stock (null when it has checked none): data_through before the end of the range you asked for
// means the newest days could not be fetched and are missing.
type BarsResponse struct {
	Symbol         string             `json:"symbol" example:"AAPL" validate:"required"`
	InstrumentName string             `json:"instrument_name" example:"Apple Inc." validate:"required"`
	Timeframe      string             `json:"timeframe" enums:"1d" example:"1d" validate:"required"`
	Adjustment     string             `json:"adjustment" enums:"raw" example:"raw" validate:"required"`
	Currency       string             `json:"currency" enums:"USD" example:"USD" validate:"required"`
	Source         string             `json:"source" example:"alpaca" validate:"required"`
	Feed           string             `json:"feed" example:"sip" validate:"required"`
	IsDemo         bool               `json:"is_demo" example:"false" validate:"required"`
	DataFrom       *string            `json:"data_from" extensions:"x-nullable" format:"date" example:"2025-11-11" validate:"required"`
	DataThrough    *string            `json:"data_through" extensions:"x-nullable" format:"date" example:"2026-11-10" validate:"required"`
	Data           []DailyBarResponse `json:"data" validate:"required"`
}

func newBarsResponse(s domain.DailyBarSeries) BarsResponse {
	out := BarsResponse{
		Symbol: s.Symbol.String(), InstrumentName: s.InstrumentName,
		Timeframe: domain.BarTimeframeDaily, Adjustment: domain.BarAdjustmentRaw, Currency: "USD",
		Source: string(s.Source), Feed: s.Feed, IsDemo: s.IsDemo(),
		Data: make([]DailyBarResponse, 0, len(s.Bars)),
	}
	if !s.DataFrom.IsZero() {
		from := s.DataFrom.String()
		out.DataFrom = &from
	}
	if !s.DataThrough.IsZero() {
		through := s.DataThrough.String()
		out.DataThrough = &through
	}
	for _, b := range s.Bars {
		out.Data = append(out.Data, DailyBarResponse{
			Date: b.Date.String(), Open: b.Open.String(), High: b.High.String(), Low: b.Low.String(),
			Close: b.Close.String(), Volume: b.Volume.String(),
		})
	}
	return out
}
