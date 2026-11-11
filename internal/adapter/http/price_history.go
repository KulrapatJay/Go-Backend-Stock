package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PriceHistoryOptions wires the daily bars endpoint. A nil Service registers no route.
type PriceHistoryOptions struct {
	Service port.PriceHistoryService
}

type priceHistoryHandlers struct {
	history  port.PriceHistoryService
	clientIP *ClientIPResolver
}

// registerPriceHistoryRoutes adds the read-only /api/v1/instruments/{symbol}/bars route behind the session
// middleware. It changes no business data (the bars it stores are a copy of market data), so it needs no
// CSRF check.
func registerPriceHistoryRoutes(app *fiber.App, opts PriceHistoryOptions, chain authChain) {
	if opts.Service == nil {
		return
	}
	h := &priceHistoryHandlers{history: opts.Service, clientIP: chain.clientIP}
	getAndHead(app, "/api/v1/instruments/:symbol/bars", chain.session, middleware.RequireAuth(), h.bars)
}

// GetInstrumentBars godoc
// @Summary      Daily price history of a stock
// @Description  The daily bars (open, high, low, close, volume) of one US stock dated from..to, oldest first, for a price chart. Prices are unadjusted (adjustment raw): the price of the day as it was traded, so the chart of a stock that split falls at the split and the history is not a basis for tax reports. Every number is a decimal string. date is the market date in New York (YYYY-MM-DD). from and to are required, both included; the range may be at most 3660 days (400 invalid_input otherwise, also for to before from). Only days that have closed in New York are included, so a to of today or later returns the days up to yesterday. A day with no bar (weekend, holiday, before the stock was listed, before the history starts at 2016-01-04) is left out, not filled with 0. The bars are a stored copy of market data shared by all users: the first request for a range the server has not seen asks the price provider and is slower, later requests are answered from the copy. A symbol the API has not seen yet is checked with the instrument provider like when recording a trade (422 unknown_symbol when it does not exist, 503 dependency_unavailable when the provider cannot answer), at most 30 new symbols per user per hour (429 rate_limited). When the price provider cannot be reached the stored bars are returned with 200 if the stock has any and data_through shows how far they go; with none stored the answer is 503 dependency_unavailable. With PRICE_PROVIDER=demo the bars are sample data and is_demo is true. Needs the session cookie.
// @Tags         Holdings/Prices
// @ID           getInstrumentBars
// @Produce      json
// @Param        symbol  path   string  true  "Ticker symbol, case-insensitive, for example AAPL or BRK.B"
// @Param        from    query  string  true  "First date, YYYY-MM-DD"  format(date)
// @Param        to      query  string  true  "Last date, YYYY-MM-DD, at most 3660 days after from"  format(date)
// @Success      200  {object}  BarsResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input: bad symbol, from or to, to before from, or a range over 3660 days"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      422  {object}  ErrorResponse  "unknown_symbol"
// @Failure      429  {object}  ErrorResponse  "rate_limited: too many new symbols this hour"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Router       /api/v1/instruments/{symbol}/bars [get]
func (h *priceHistoryHandlers) bars(c fiber.Ctx) error {
	symbol, err := domain.ParseSymbol(c.Params("symbol"))
	if err != nil {
		return err
	}
	from, err := queryDate(c, "from")
	if err != nil {
		return err
	}
	to, err := queryDate(c, "to")
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	meta, err := requestMeta(c, h.clientIP)
	if err != nil {
		return err
	}
	series, err := h.history.Bars(c.Context(), p, symbol, from, to, meta)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(newBarsResponse(series))
}

// queryDate reads a required YYYY-MM-DD query parameter.
func queryDate(c fiber.Ctx, name string) (domain.TradingDate, error) {
	raw := c.Query(name)
	if raw == "" {
		return domain.TradingDate{}, domain.NewError(domain.CodeInvalidInput, name+" is required")
	}
	return domain.ParseTradingDate(name, raw)
}
