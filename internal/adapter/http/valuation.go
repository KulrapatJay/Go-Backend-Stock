package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// ValuationOptions wires the holdings and valuation endpoints. A nil Service registers no route.
type ValuationOptions struct {
	Service port.ValuationService
}

type valuationHandlers struct {
	valuation port.ValuationService
}

// registerValuationRoutes adds the read-only /api/v1/portfolios/{portfolio_id}/holdings and
// /valuation routes and the account summary /api/v1/valuation behind the session middleware. They
// change nothing, so they need no CSRF check.
func registerValuationRoutes(app *fiber.App, opts ValuationOptions, chain authChain) {
	if opts.Service == nil {
		return
	}
	h := &valuationHandlers{valuation: opts.Service}
	require := middleware.RequireAuth()

	getAndHead(app, "/api/v1/portfolios/:portfolio_id/holdings", chain.session, require, h.holdings)
	getAndHead(app, "/api/v1/portfolios/:portfolio_id/valuation", chain.session, require, h.value)
	getAndHead(app, "/api/v1/valuation", chain.session, require, h.valueAll)
}

// ListHoldings godoc
// @Summary      List what a portfolio holds
// @Description  One entry per stock ever traded in the portfolio, sorted by symbol, calculated from its transactions with a moving weighted-average cost. A stock that was sold out stays with quantity "0" because of its realized profit and loss. No prices are used, so this works even when the price provider does not. Numbers are exact decimal strings; round them for display only. A portfolio that belongs to someone else gets exactly the same 404 as one that does not exist. Needs the session cookie.
// @Tags         Holdings/Prices
// @ID           listHoldings
// @Produce      json
// @Param        portfolio_id  path  string  true  "Portfolio ID"  format(uuid)
// @Success      200  {object}  HoldingsResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input: portfolio_id is not a UUID"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      404  {object}  ErrorResponse  "not_found"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Router       /api/v1/portfolios/{portfolio_id}/holdings [get]
func (h *valuationHandlers) holdings(c fiber.Ctx) error {
	id, err := domain.ParseID("portfolio_id", c.Params("portfolio_id"))
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	found, err := h.valuation.Holdings(c.Context(), p, id)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(newHoldingsResponse(found))
}

// GetValuation godoc
// @Summary      Value a portfolio with delayed prices
// @Description  Prices every stock still held with a delayed reference price: the close of the last bar before 15 minutes ago. It is not a bid or ask price and not a price one could trade at; each quote says so in price_type (delayed_bar_close) and delay_minutes (15), so label the price from those fields. A price that is missing is null and never "0": coverage says whether every stock still held has a price (complete), some do (partial) or none does (unavailable, which is also the answer when the price provider cannot be reached). Unless coverage is complete, market_value, unrealized_pnl and unrealized_pnl_percent are null and priced_market_value is only the subtotal of the priced stocks; realized_pnl, cost_basis and the holdings never depend on prices. unrealized_pnl = quantity x price - remaining cost, without any selling fee. unrealized_pnl_percent = unrealized_pnl / cost_basis x 100 and weight_percent = market_value / market_value of the portfolio x 100; both are in percent ("8.5" is 8.5%), exact and not rounded, and null when they cannot be known (no price, a stock that was sold out for unrealized_pnl_percent, coverage that is not complete for weight_percent): null never means 0%. With PRICE_PROVIDER=demo the prices are sample data and is_demo is true. calculated_at is when the transactions were read. A portfolio that belongs to someone else gets exactly the same 404 as one that does not exist. Needs the session cookie.
// @Tags         Holdings/Prices
// @ID           getValuation
// @Produce      json
// @Param        portfolio_id  path  string  true  "Portfolio ID"  format(uuid)
// @Success      200  {object}  ValuationResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input: portfolio_id is not a UUID"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      404  {object}  ErrorResponse  "not_found"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Router       /api/v1/portfolios/{portfolio_id}/valuation [get]
func (h *valuationHandlers) value(c fiber.Ctx) error {
	id, err := domain.ParseID("portfolio_id", c.Params("portfolio_id"))
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	valued, err := h.valuation.Value(c.Context(), p, id)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(newValuationResponse(valued))
}

// GetAccountValuation godoc
// @Summary      Value every portfolio of the user together
// @Description  The summary for a dashboard: every portfolio of the signed-in user valued with the same delayed reference prices as GET /api/v1/portfolios/{portfolio_id}/valuation and added up. Each stock is priced once per request, however many portfolios hold it. portfolios has one entry per portfolio (newest first, not paged, because the totals count all of them) with the totals of its own valuation and its share of the account; allocation merges the same stock from every portfolio into one row, the largest market_value first, for a chart of what the account holds. The rules for missing prices are those of a single portfolio, over all of them: coverage is complete only when every stock still held in any portfolio has a price; unless it is, market_value, unrealized_pnl and unrealized_pnl_percent are null (never "0") and priced_market_value is only the subtotal of the priced stocks, while cost_basis and realized_pnl never depend on prices. Each portfolio keeps its own coverage, so one portfolio can be complete while the account is partial. Percentages are in percent ("8.5" is 8.5%), exact and not rounded; weight_percent is null unless the coverage of the account is complete. When the price provider cannot be reached the answer is still 200 with coverage unavailable. A user without portfolios or trades gets zeros and empty lists. Only the caller's own portfolios are ever counted. Needs the session cookie.
// @Tags         Holdings/Prices
// @ID           getAccountValuation
// @Produce      json
// @Success      200  {object}  AccountValuationResponse
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Router       /api/v1/valuation [get]
func (h *valuationHandlers) valueAll(c fiber.Ctx) error {
	p, _ := middleware.GetPrincipal(c)
	valued, err := h.valuation.ValueAll(c.Context(), p)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(newAccountValuationResponse(valued))
}
