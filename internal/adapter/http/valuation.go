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
// /valuation routes behind the session middleware. They change nothing, so they need no CSRF check.
func registerValuationRoutes(app *fiber.App, opts ValuationOptions, chain authChain) {
	if opts.Service == nil {
		return
	}
	h := &valuationHandlers{valuation: opts.Service}
	require := middleware.RequireAuth()

	getAndHead(app, "/api/v1/portfolios/:portfolio_id/holdings", chain.session, require, h.holdings)
	getAndHead(app, "/api/v1/portfolios/:portfolio_id/valuation", chain.session, require, h.value)
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
// @Description  Prices every stock still held with a delayed reference price: the close of the last bar before 15 minutes ago. It is not a bid or ask price and not a price one could trade at. A price that is missing is null and never "0": coverage says whether every stock still held has a price (complete), some do (partial) or none does (unavailable, which is also the answer when the price provider cannot be reached). Unless coverage is complete, market_value and unrealized_pnl are null and priced_market_value is only the subtotal of the priced stocks; realized_pnl and the holdings never depend on prices. unrealized_pnl = quantity x price - remaining cost, without any selling fee. With PRICE_PROVIDER=demo the prices are sample data and is_demo is true. calculated_at is when the transactions were read. A portfolio that belongs to someone else gets exactly the same 404 as one that does not exist. Needs the session cookie.
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
