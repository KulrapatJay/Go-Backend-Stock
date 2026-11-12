package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PerformanceOptions wires the performance endpoints. A nil Service registers no route.
type PerformanceOptions struct {
	Service port.PerformanceService
}

type performanceHandlers struct {
	performance port.PerformanceService
	clientIP    *ClientIPResolver
}

// registerPerformanceRoutes adds the read-only /api/v1/portfolios/{portfolio_id}/performance and
// /api/v1/performance routes behind the session middleware. They change no business data (the bars they
// store are a copy of market data), so they need no CSRF check.
func registerPerformanceRoutes(app *fiber.App, opts PerformanceOptions, chain authChain) {
	if opts.Service == nil {
		return
	}
	h := &performanceHandlers{performance: opts.Service, clientIP: chain.clientIP}
	require := middleware.RequireAuth()
	getAndHead(app, "/api/v1/portfolios/:portfolio_id/performance", chain.session, require, h.portfolio)
	getAndHead(app, "/api/v1/performance", chain.session, require, h.account)
}

// GetPortfolioPerformance godoc
// @Summary      Daily value and return of a portfolio against SPY and QQQ
// @Description  The performance of one of the caller's portfolios. The daily value of the shares held at the end of each day and the cumulative time-weighted return since the first day, for a price chart against an index. range is required: 1m, 3m, 6m, ytd, 1y, 5y or all (all goes back to the first trade, at most 3660 days); it ends on the last day that has closed in New York. benchmarks is an optional comma-separated list of SPY and QQQ (no spaces, no repeats); without it the answer has no benchmark. Every number is a decimal string and every _percent is in percent ("8.5" is 8.5%), exact and not rounded. method is time_weighted: a day's return is (value at the end of the day + money from sales) / (value at the end of the previous day + money spent on purchases) - 1, where a purchase counts at the start of the day and a sale at its end, both with their fee (fees_included is true), so buying or selling more does not make the line jump but the gap between the price paid and the day's close, and the fees, are part of the return. The line starts on the first day of the range on which the portfolio is worth something, with return_percent "0" (range.from is that day; what happened on it is not in the line), and the benchmarks start on the same day at 0. While nothing is held the line is flat: market_value "0" and the return of the last day. A day on which a stock still held has no bar has market_value and return_percent null, never 0, and coverage is partial; the next day with a value links the return across the gap. coverage is unavailable when something was held but no day could be valued (the points are then empty) and complete with no points when nothing was held. Prices are unadjusted (adjustment raw), so a stock split makes the value of the days after it wrong until splits are supported, and dividends are not included, for the portfolio and for the benchmarks, which are the ETFs SPY and QQQ. A trade made on a weekend or a holiday counts on the next day that has a bar. Points are daily (interval 1d). The bars come from a stored copy of market data: the first request for a long range asks the price provider and can be slow, and one that does not finish in time answers with what was stored (coverage partial and data_through before range.to) or, when a stock has nothing stored, 503 dependency_unavailable; asking again continues where it stopped. A benchmark the API has not seen yet is checked with the instrument provider within the per-user limit of 30 new symbols per hour (429 rate_limited). With PRICE_PROVIDER=demo the prices are sample data and is_demo is true. This is a way to follow your own records, not a report for tax and not investment advice. A portfolio that belongs to someone else gets exactly the same 404 as one that does not exist. Needs the session cookie.
// @Tags         Holdings/Prices
// @ID           getPortfolioPerformance
// @Produce      json
// @Param        portfolio_id  path   string  true   "Portfolio ID"  format(uuid)
// @Param        range         query  string  true   "How far back to look"  Enums(1m, 3m, 6m, ytd, 1y, 5y, all)
// @Param        benchmarks    query  string  false  "Comma-separated benchmarks to compare with, from SPY and QQQ, for example SPY,QQQ"
// @Success      200  {object}  PerformanceResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input: portfolio_id is not a UUID, range is missing or unknown, benchmarks has an unknown or repeated symbol"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      404  {object}  ErrorResponse  "not_found"
// @Failure      429  {object}  ErrorResponse  "rate_limited: too many new symbols this hour"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Router       /api/v1/portfolios/{portfolio_id}/performance [get]
func (h *performanceHandlers) portfolio(c fiber.Ctx) error {
	id, err := domain.ParseID("portfolio_id", c.Params("portfolio_id"))
	if err != nil {
		return err
	}
	q, err := performanceQuery(c)
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	meta, err := requestMeta(c, h.clientIP)
	if err != nil {
		return err
	}
	report, err := h.performance.Portfolio(c.Context(), p, id, q, meta)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(newPerformanceResponse(report))
}

// GetAccountPerformance godoc
// @Summary      Daily value and return of the whole account against SPY and QQQ
// @Description  The daily value of the shares held at the end of each day and the cumulative time-weighted return since the first day, for a price chart against an index. range is required: 1m, 3m, 6m, ytd, 1y, 5y or all (all goes back to the first trade, at most 3660 days); it ends on the last day that has closed in New York. benchmarks is an optional comma-separated list of SPY and QQQ (no spaces, no repeats); without it the answer has no benchmark. Every number is a decimal string and every _percent is in percent ("8.5" is 8.5%), exact and not rounded. method is time_weighted: a day's return is (value at the end of the day + money from sales) / (value at the end of the previous day + money spent on purchases) - 1, where a purchase counts at the start of the day and a sale at its end, both with their fee (fees_included is true), so buying or selling more does not make the line jump but the gap between the price paid and the day's close, and the fees, are part of the return. The line starts on the first day of the range on which the portfolio is worth something, with return_percent "0" (range.from is that day; what happened on it is not in the line), and the benchmarks start on the same day at 0. While nothing is held the line is flat: market_value "0" and the return of the last day. A day on which a stock still held has no bar has market_value and return_percent null, never 0, and coverage is partial; the next day with a value links the return across the gap. coverage is unavailable when something was held but no day could be valued (the points are then empty) and complete with no points when nothing was held. Prices are unadjusted (adjustment raw), so a stock split makes the value of the days after it wrong until splits are supported, and dividends are not included, for the portfolio and for the benchmarks, which are the ETFs SPY and QQQ. A trade made on a weekend or a holiday counts on the next day that has a bar. Points are daily (interval 1d). The bars come from a stored copy of market data: the first request for a long range asks the price provider and can be slow, and one that does not finish in time answers with what was stored (coverage partial and data_through before range.to) or, when a stock has nothing stored, 503 dependency_unavailable; asking again continues where it stopped. A benchmark the API has not seen yet is checked with the instrument provider within the per-user limit of 30 new symbols per hour (429 rate_limited). With PRICE_PROVIDER=demo the prices are sample data and is_demo is true. This is a way to follow your own records, not a report for tax and not investment advice. The portfolios of the caller are added together: the same stock held in two portfolios counts once with the sum of the shares, and the portfolios of other users are never counted. Needs the session cookie.
// @Tags         Holdings/Prices
// @ID           getAccountPerformance
// @Produce      json
// @Param        range       query  string  true   "How far back to look"  Enums(1m, 3m, 6m, ytd, 1y, 5y, all)
// @Param        benchmarks  query  string  false  "Comma-separated benchmarks to compare with, from SPY and QQQ, for example SPY,QQQ"
// @Success      200  {object}  PerformanceResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input: range is missing or unknown, benchmarks has an unknown or repeated symbol"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      429  {object}  ErrorResponse  "rate_limited: too many new symbols this hour"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Router       /api/v1/performance [get]
func (h *performanceHandlers) account(c fiber.Ctx) error {
	q, err := performanceQuery(c)
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	meta, err := requestMeta(c, h.clientIP)
	if err != nil {
		return err
	}
	report, err := h.performance.Account(c.Context(), p, q, meta)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(newPerformanceResponse(report))
}

// performanceQuery reads the range and the benchmarks of a request.
func performanceQuery(c fiber.Ctx) (port.PerformanceQuery, error) {
	r, err := domain.ParsePerformanceRange(c.Query("range"))
	if err != nil {
		return port.PerformanceQuery{}, err
	}
	b, err := domain.ParseBenchmarks(c.Query("benchmarks"))
	if err != nil {
		return port.PerformanceQuery{}, err
	}
	return port.PerformanceQuery{Range: r, Benchmarks: b}, nil
}
