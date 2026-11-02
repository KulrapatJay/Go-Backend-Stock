package http

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PortfolioOptions wires the portfolio endpoints. A nil Service registers no portfolio route.
type PortfolioOptions struct {
	Service port.PortfolioService
}

type portfolioHandlers struct {
	portfolios port.PortfolioService
	clientIP   *ClientIPResolver
}

// registerPortfolioRoutes adds the /api/v1/portfolios routes behind the same session and CSRF
// middleware as the auth routes: resolve session → require auth → CSRF/Origin → handler.
func registerPortfolioRoutes(app *fiber.App, opts PortfolioOptions, chain authChain) {
	if opts.Service == nil {
		return
	}
	h := &portfolioHandlers{portfolios: opts.Service, clientIP: chain.clientIP}
	require := middleware.RequireAuth()

	app.Post("/api/v1/portfolios", chain.session, require, chain.csrf, h.create)
	app.Get("/api/v1/portfolios", chain.session, require, h.list)
	app.Get("/api/v1/portfolios/:portfolio_id", chain.session, require, h.get)
}

// CreatePortfolio godoc
// @Summary      Create a portfolio
// @Description  Adds a portfolio for the signed-in user. A user can have many portfolios (for example long term, short term and speculative); every portfolio uses USD. The name is trimmed, must be 1-100 characters and must not repeat another portfolio name of the same user, ignoring letter case (409 portfolio_name_conflict); another user may use the same name. The ETag header carries the version to send in If-Match when you change or delete the portfolio. Needs the session cookie and X-CSRF-Token.
// @Tags         Portfolios
// @ID           createPortfolio
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string            true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    PortfolioRequest  true  "Name and optional description"
// @Success      201  {object}  PortfolioResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input or malformed_json"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      409  {object}  ErrorResponse  "portfolio_name_conflict"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable"
// @Header       201  {string}  ETag  "The portfolio version in quotes, for example \"1\""
// @Router       /api/v1/portfolios [post]
func (h *portfolioHandlers) create(c fiber.Ctx) error {
	var req PortfolioRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	in, err := parsePortfolioRequest(req)
	if err != nil {
		return err
	}
	meta, err := requestMeta(c, h.clientIP)
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	created, err := h.portfolios.Create(c.Context(), p, in, meta)
	if err != nil {
		return err
	}
	return sendPortfolio(c, fiber.StatusCreated, created)
}

// ListPortfolios godoc
// @Summary      List the portfolios of the signed-in user
// @Description  Newest first. Pass the next_cursor of a page as cursor to get the following page; next_cursor is null on the last page. A cursor is opaque and only valid for the user it was issued to. Other users' portfolios never appear.
// @Tags         Portfolios
// @ID           listPortfolios
// @Produce      json
// @Param        limit   query  int     false  "Page size, 1-100 (default 20)"  minimum(1)  maximum(100)  default(20)
// @Param        cursor  query  string  false  "next_cursor of the previous page"
// @Success      200  {object}  PortfolioPageResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input: bad limit or cursor"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable"
// @Router       /api/v1/portfolios [get]
func (h *portfolioHandlers) list(c fiber.Ctx) error {
	page, err := domain.ParsePage(strings.Clone(c.Query("limit")), strings.Clone(c.Query("cursor")))
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	found, err := h.portfolios.List(c.Context(), p, page)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(newPortfolioPageResponse(found))
}

// GetPortfolio godoc
// @Summary      Get one portfolio
// @Description  A portfolio that belongs to someone else gets exactly the same 404 as one that does not exist. The ETag header carries the version to send in If-Match.
// @Tags         Portfolios
// @ID           getPortfolio
// @Produce      json
// @Param        portfolio_id  path  string  true  "Portfolio ID"  format(uuid)
// @Success      200  {object}  PortfolioResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input: portfolio_id is not a UUID"
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      404  {object}  ErrorResponse  "not_found"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable"
// @Header       200  {string}  ETag  "The portfolio version in quotes, for example \"1\""
// @Router       /api/v1/portfolios/{portfolio_id} [get]
func (h *portfolioHandlers) get(c fiber.Ctx) error {
	id, err := domain.ParseID("portfolio_id", c.Params("portfolio_id"))
	if err != nil {
		return err
	}
	p, _ := middleware.GetPrincipal(c)
	found, err := h.portfolios.Get(c.Context(), p, id)
	if err != nil {
		return err
	}
	return sendPortfolio(c, fiber.StatusOK, found)
}

func parsePortfolioRequest(req PortfolioRequest) (domain.PortfolioInput, error) {
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	return domain.ParsePortfolioInput(req.Name, description)
}

// sendPortfolio answers with one portfolio, its ETag and no-store: the version and the data behind
// it must not be served from a cache.
func sendPortfolio(c fiber.Ctx, status int, p domain.Portfolio) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	setETag(c, p.Version)
	return c.Status(status).JSON(newPortfolioResponse(p))
}
