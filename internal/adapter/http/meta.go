package http

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

// requestMeta collects what a use case may know about the request. Every value is copied out of the
// request, which Fiber reuses after the handler returns.
func (h *authHandlers) requestMeta(c fiber.Ctx) (domain.RequestMeta, error) {
	id, err := domain.ParseID("request_id", middleware.GetRequestID(c))
	if err != nil {
		return domain.RequestMeta{}, domain.NewError(domain.CodeInternal, "request has no valid request ID")
	}
	var forwarded []string
	for _, v := range c.Request().Header.PeekAll(fiber.HeaderXForwardedFor) {
		forwarded = append(forwarded, string(v))
	}
	ip := h.clientIP.Resolve(c.RequestCtx().RemoteAddr().String(), forwarded)
	return domain.NewRequestMeta(id, ip, strings.Clone(c.Get(fiber.HeaderUserAgent))), nil
}
