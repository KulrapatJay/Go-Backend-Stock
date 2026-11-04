package http

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// TransactionOptions wires the transaction endpoints. A nil Service registers no transaction route.
// Clock gives the latest allowed executed_at; it is required when Service is set.
type TransactionOptions struct {
	Service port.TransactionService
	Clock   port.Clock
}

type transactionHandlers struct {
	transactions port.TransactionService
	clock        port.Clock
	clientIP     *ClientIPResolver
}

// registerTransactionRoutes adds the /api/v1/portfolios/{portfolio_id}/transactions routes behind
// the same session and CSRF middleware as the portfolios: resolve session → require auth →
// CSRF/Origin (unsafe methods) → handler.
func registerTransactionRoutes(app *fiber.App, opts TransactionOptions, chain authChain) {
	if opts.Service == nil {
		return
	}
	if opts.Clock == nil {
		panic("http: transaction routes need TransactionOptions.Clock to check executed_at")
	}
	h := &transactionHandlers{transactions: opts.Service, clock: opts.Clock, clientIP: chain.clientIP}
	require := middleware.RequireAuth()

	app.Post("/api/v1/portfolios/:portfolio_id/transactions", chain.session, require, chain.csrf, h.create)
	getAndHead(app, "/api/v1/portfolios/:portfolio_id/transactions", chain.session, require, h.list)
	getAndHead(app, "/api/v1/portfolios/:portfolio_id/transactions/:transaction_id", chain.session, require, h.get)
	app.Put("/api/v1/portfolios/:portfolio_id/transactions/:transaction_id", chain.session, require, chain.csrf, h.update)
	app.Delete("/api/v1/portfolios/:portfolio_id/transactions/:transaction_id", chain.session, require, chain.csrf, h.delete)
}

// CreateTransaction godoc
// @Summary      Record a trade in a portfolio
// @Description  Records a buy or sell the user made elsewhere; this API never places an order. Every field is required. quantity, price and fee are decimal strings (a JSON number is refused with 400 malformed_json): quantity and price must be greater than zero, fee may be "0", and each has at most 10 decimals and 18 digits before the point. executed_at is an RFC 3339 timestamp with a timezone, not in the future and with at most microsecond precision. The symbol is upper-cased; a symbol the API has not seen yet is checked with the instrument provider (422 unknown_symbol when it does not exist, 503 dependency_unavailable when the provider cannot answer, so retry), at most 30 new symbols per user per hour (429 rate_limited). The whole history of the portfolio is replayed in time order: a sale that would leave fewer than zero shares at any point gives 422 insufficient_shares and nothing is written. Idempotency-Key (1-128 visible ASCII characters) is required: the same key with the same trade returns the first result again with 201 and records nothing more; the same key with another trade gives 409 idempotency_conflict. A key keeps its result for 24 hours from the first request. The ETag header carries the version to send in If-Match when the trade is changed or deleted. A portfolio of another user gives the same 404 as one that does not exist. Needs the session cookie and X-CSRF-Token.
// @Tags         Transactions
// @ID           createTransaction
// @Accept       json
// @Produce      json
// @Param        portfolio_id     path    string              true  "Portfolio ID"  format(uuid)
// @Param        Idempotency-Key  header  string              true  "Unique key of this request, 1-128 visible ASCII characters, for example a UUID"  minLength(1)  maxLength(128)
// @Param        X-CSRF-Token     header  string              true  "Token from GET /api/v1/auth/csrf"
// @Param        body             body    TransactionRequest  true  "The trade; every field is required and numbers are decimal strings"
// @Success      201  {object}  TransactionResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input (body, portfolio_id or Idempotency-Key) or malformed_json"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      404  {object}  ErrorResponse  "not_found"
// @Failure      409  {object}  ErrorResponse  "idempotency_conflict: the key was used for another trade"
// @Failure      422  {object}  ErrorResponse  "unknown_symbol or insufficient_shares"
// @Failure      429  {object}  ErrorResponse  "rate_limited: too many new symbols this hour"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Header       201  {string}  ETag  "The transaction version number wrapped in double quotes"
// @Router       /api/v1/portfolios/{portfolio_id}/transactions [post]
func (h *transactionHandlers) create(c fiber.Ctx) error {
	portfolioID, err := domain.ParseID("portfolio_id", c.Params("portfolio_id"))
	if err != nil {
		return err
	}
	// A bad key is refused before the body is read.
	key, err := domain.ParseIdempotencyKey(strings.Clone(c.Get("Idempotency-Key")))
	if err != nil {
		return err
	}
	var req TransactionRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	in, err := domain.ParseTransactionInput(req.Symbol, req.Side, req.Quantity, req.Price, req.Fee, req.ExecutedAt, h.clock.Now())
	if err != nil {
		return err
	}
	meta, err := requestMeta(c, h.clientIP)
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	created, err := h.transactions.Create(c.Context(), p, portfolioID, key, in, meta)
	if err != nil {
		return err
	}
	return sendTransaction(c, fiber.StatusCreated, created)
}

// ListTransactions godoc
// @Summary      List the trades of a portfolio
// @Description  Newest first by executed_at, trades executed at the same time by sequence. Pass the next_cursor of a page as cursor to get the following page; next_cursor is null on the last page. A cursor is opaque and only valid for the portfolio and the filters it was issued for: with other filters it gives 400. symbol, side, from and to narrow the list; an empty value does not filter. from and to include the instant itself. Write a timezone offset such as +02:00 as %2B02:00 in the query. A portfolio of another user gives the same 404 as one that does not exist.
// @Tags         Transactions
// @ID           listTransactions
// @Produce      json
// @Param        portfolio_id  path   string  true   "Portfolio ID"  format(uuid)
// @Param        limit         query  int     false  "Page size, 1-100 (default 20)"  minimum(1)  maximum(100)  default(20)
// @Param        cursor        query  string  false  "next_cursor of the previous page"
// @Param        symbol        query  string  false  "Only this symbol, for example AAPL"
// @Param        side          query  string  false  "Only this side"  Enums(buy, sell)
// @Param        from          query  string  false  "Only trades executed at or after this RFC 3339 time"  format(date-time)
// @Param        to            query  string  false  "Only trades executed at or before this RFC 3339 time"  format(date-time)
// @Success      200  {object}  TransactionPageResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input: bad portfolio_id, limit, cursor, symbol, side, from or to"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      404  {object}  ErrorResponse  "not_found"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Router       /api/v1/portfolios/{portfolio_id}/transactions [get]
func (h *transactionHandlers) list(c fiber.Ctx) error {
	portfolioID, err := domain.ParseID("portfolio_id", c.Params("portfolio_id"))
	if err != nil {
		return err
	}
	filter, err := parseTransactionFilter(c)
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	found, err := h.transactions.List(c.Context(), p, portfolioID, filter)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(newTransactionPageResponse(found))
}

// GetTransaction godoc
// @Summary      Get one trade
// @Description  A trade in a portfolio of another user, or in another portfolio of yours, gets exactly the same 404 as one that does not exist. The ETag header carries the version to send in If-Match.
// @Tags         Transactions
// @ID           getTransaction
// @Produce      json
// @Param        portfolio_id    path  string  true  "Portfolio ID"  format(uuid)
// @Param        transaction_id  path  string  true  "Transaction ID"  format(uuid)
// @Success      200  {object}  TransactionResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input: portfolio_id or transaction_id is not a UUID"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      404  {object}  ErrorResponse  "not_found"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Header       200  {string}  ETag  "The transaction version number wrapped in double quotes"
// @Router       /api/v1/portfolios/{portfolio_id}/transactions/{transaction_id} [get]
func (h *transactionHandlers) get(c fiber.Ctx) error {
	portfolioID, err := domain.ParseID("portfolio_id", c.Params("portfolio_id"))
	if err != nil {
		return err
	}
	transactionID, err := domain.ParseID("transaction_id", c.Params("transaction_id"))
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	found, err := h.transactions.Get(c.Context(), p, portfolioID, transactionID)
	if err != nil {
		return err
	}
	return sendTransaction(c, fiber.StatusOK, found)
}

// UpdateTransaction godoc
// @Summary      Replace a trade
// @Description  Replaces the whole trade: send all six fields (symbol, side, quantity, price, fee, executed_at) with the same rules as when recording it; a body that leaves a field out is refused with 400 and nothing is written. Send the ETag of your last read in If-Match: without it the call is refused with 428 precondition_required; if the trade changed since (version_conflict) the call is refused with 412 and nothing is written. Only the exact form of one version number wrapped in double quotes is accepted: the wildcard, weak validators and lists give 400. The trade stays in its portfolio and keeps its id, sequence and created_at; the version goes up by one. A symbol the API has not seen yet is checked with the instrument provider (422 unknown_symbol, 503 dependency_unavailable, 429 rate_limited) like when recording a trade. The whole history of the portfolio is replayed in time order: a change that would leave fewer than zero shares at any point gives 422 invalid_history and nothing is written. A trade that is not in this portfolio, or in a portfolio of another user, gets the same 404 as one that does not exist. Needs the session cookie and X-CSRF-Token.
// @Tags         Transactions
// @ID           updateTransaction
// @Accept       json
// @Produce      json
// @Param        portfolio_id    path    string              true  "Portfolio ID"  format(uuid)
// @Param        transaction_id  path    string              true  "Transaction ID"  format(uuid)
// @Param        If-Match        header  string              true  "ETag of the last read: the version number wrapped in double quotes"
// @Param        X-CSRF-Token    header  string              true  "Token from GET /api/v1/auth/csrf"
// @Param        body            body    TransactionRequest  true  "The whole trade; every field is required and numbers are decimal strings"
// @Success      200  {object}  TransactionResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input (body, portfolio_id, transaction_id or If-Match) or malformed_json"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      404  {object}  ErrorResponse  "not_found"
// @Failure      412  {object}  ErrorResponse  "version_conflict: read the trade again"
// @Failure      422  {object}  ErrorResponse  "unknown_symbol or invalid_history"
// @Failure      428  {object}  ErrorResponse  "precondition_required: send If-Match"
// @Failure      429  {object}  ErrorResponse  "rate_limited: too many new symbols this hour"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Header       200  {string}  ETag  "The new version number wrapped in double quotes"
// @Router       /api/v1/portfolios/{portfolio_id}/transactions/{transaction_id} [put]
func (h *transactionHandlers) update(c fiber.Ctx) error {
	portfolioID, err := domain.ParseID("portfolio_id", c.Params("portfolio_id"))
	if err != nil {
		return err
	}
	transactionID, err := domain.ParseID("transaction_id", c.Params("transaction_id"))
	if err != nil {
		return err
	}
	expected, err := parseIfMatch(c)
	if err != nil {
		return err
	}
	var req TransactionRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	in, err := domain.ParseTransactionInput(req.Symbol, req.Side, req.Quantity, req.Price, req.Fee, req.ExecutedAt, h.clock.Now())
	if err != nil {
		return err
	}
	meta, err := requestMeta(c, h.clientIP)
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	updated, err := h.transactions.Update(c.Context(), p, portfolioID, transactionID, expected, in, meta)
	if err != nil {
		return err
	}
	return sendTransaction(c, fiber.StatusOK, updated)
}

// DeleteTransaction godoc
// @Summary      Delete a trade
// @Description  Removes the trade and replays the history of the portfolio: a deletion that would leave fewer than zero shares at any point (for example removing a buy that a later sale needs) gives 422 invalid_history and nothing is written. Needs If-Match with the ETag of your last read, like PUT. A trade that is not in this portfolio, or in a portfolio of another user, gets the same 404 as one that does not exist, and so does a trade that was already deleted. Needs the session cookie and X-CSRF-Token.
// @Tags         Transactions
// @ID           deleteTransaction
// @Produce      json
// @Param        portfolio_id    path    string  true  "Portfolio ID"  format(uuid)
// @Param        transaction_id  path    string  true  "Transaction ID"  format(uuid)
// @Param        If-Match        header  string  true  "ETag of the last read: the version number wrapped in double quotes"
// @Param        X-CSRF-Token    header  string  true  "Token from GET /api/v1/auth/csrf"
// @Success      204  "Deleted"
// @Failure      400  {object}  ErrorResponse  "invalid_input (portfolio_id, transaction_id or If-Match)"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      404  {object}  ErrorResponse  "not_found"
// @Failure      412  {object}  ErrorResponse  "version_conflict: read the trade again"
// @Failure      422  {object}  ErrorResponse  "invalid_history"
// @Failure      428  {object}  ErrorResponse  "precondition_required: send If-Match"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       503  {integer}  Retry-After  "Seconds to wait; only sent with server_busy, not with dependency_unavailable"
// @Router       /api/v1/portfolios/{portfolio_id}/transactions/{transaction_id} [delete]
func (h *transactionHandlers) delete(c fiber.Ctx) error {
	portfolioID, err := domain.ParseID("portfolio_id", c.Params("portfolio_id"))
	if err != nil {
		return err
	}
	transactionID, err := domain.ParseID("transaction_id", c.Params("transaction_id"))
	if err != nil {
		return err
	}
	expected, err := parseIfMatch(c)
	if err != nil {
		return err
	}
	meta, err := requestMeta(c, h.clientIP)
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	if err := h.transactions.Delete(c.Context(), p, portfolioID, transactionID, expected, meta); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// parseTransactionFilter reads limit, cursor, symbol, side, from and to. An empty value does not
// filter. Every value is copied out of the request, which Fiber reuses after the handler returns.
func parseTransactionFilter(c fiber.Ctx) (domain.TransactionFilter, error) {
	page, err := domain.ParsePage(strings.Clone(c.Query("limit")), strings.Clone(c.Query("cursor")))
	if err != nil {
		return domain.TransactionFilter{}, err
	}
	var symbol *domain.Symbol
	if raw := strings.Clone(c.Query("symbol")); raw != "" {
		s, err := domain.ParseSymbol(raw)
		if err != nil {
			return domain.TransactionFilter{}, err
		}
		symbol = &s
	}
	var side *domain.Side
	if raw := strings.Clone(c.Query("side")); raw != "" {
		s, err := domain.ParseSide(raw)
		if err != nil {
			return domain.TransactionFilter{}, err
		}
		side = &s
	}
	from, err := parseTimeQuery(c, "from")
	if err != nil {
		return domain.TransactionFilter{}, err
	}
	to, err := parseTimeQuery(c, "to")
	if err != nil {
		return domain.TransactionFilter{}, err
	}
	return domain.NewTransactionFilter(page, symbol, side, from, to)
}

func parseTimeQuery(c fiber.Ctx, name string) (*time.Time, error) {
	raw := strings.Clone(c.Query(name))
	if raw == "" {
		return nil, nil
	}
	t, err := domain.ParseTimeFilter(name, raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// sendTransaction answers with one transaction, its ETag and no-store: the version and the data
// behind it must not be served from a cache.
func sendTransaction(c fiber.Ctx, status int, t domain.Transaction) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	setETag(c, t.Version)
	return c.Status(status).JSON(newTransactionResponse(t))
}
