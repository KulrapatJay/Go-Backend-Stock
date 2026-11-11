package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// PriceHistoryService is the inbound port for the daily price history of a stock. It needs an authenticated
// principal (a zero one returns domain.ErrUnauthenticated); the bars themselves are market data shared by all
// users, so nothing here is owned by a portfolio.
type PriceHistoryService interface {
	// Bars returns the unadjusted daily bars of symbol dated from..to, oldest first, from the stored copy,
	// asking the price provider first for the days it has never been asked about. A day that has not closed
	// in New York is never included, so to is cut back to the last closed day. A symbol the API has not seen
	// yet is checked with the instrument provider like when recording a trade (domain.ErrUnknownSymbol,
	// domain.ErrDependencyUnavailable, a rate_limited error past the per-user lookup limit). A range longer
	// than domain.MaxBarRangeDays or with to before from is invalid_input. When the price provider cannot
	// answer, the stored bars are returned if the stock has any (DataThrough tells how far they go);
	// otherwise the error is domain.ErrDependencyUnavailable.
	Bars(ctx context.Context, p domain.Principal, symbol domain.Symbol, from, to domain.TradingDate, meta domain.RequestMeta) (domain.DailyBarSeries, error)
}
