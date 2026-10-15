package http

import (
	"context"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
)

// ReadinessChecker reports whether a dependency required to serve traffic is reachable.
type ReadinessChecker interface {
	Ping(ctx context.Context) error
}

// HealthResponse is returned by /healthz.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// ReadyResponse is returned by /readyz when the API can serve traffic.
type ReadyResponse struct {
	Status string `json:"status" example:"ready"`
}

// HealthHandler serves liveness and readiness probes.
type HealthHandler struct {
	readiness ReadinessChecker
	log       *slog.Logger
	budget    time.Duration
}

// Healthz godoc
// @Summary      Liveness probe
// @Description  Returns 200 while the process is running. Does not check the database.
// @Tags         Health
// @ID           getHealthz
// @Produce      json
// @Success      200  {object}  HealthResponse
// @Header       200  {string}  X-Request-ID  "Server-generated UUIDv7 for this request"
// @Router       /healthz [get]
func (h HealthHandler) Healthz(c fiber.Ctx) error {
	return c.JSON(HealthResponse{Status: "ok"})
}

// Readyz godoc
// @Summary      Readiness probe
// @Description  Pings the database within a 1 second budget. Returns 503 not_ready without connection details when it is unreachable.
// @Tags         Health
// @ID           getReadyz
// @Produce      json
// @Success      200  {object}  ReadyResponse
// @Failure      503  {object}  ErrorResponse
// @Header       200  {string}  X-Request-ID  "Server-generated UUIDv7 for this request"
// @Router       /readyz [get]
func (h HealthHandler) Readyz(c fiber.Ctx) error {
	// Detached from fiber.Ctx on purpose: the context is bounded by budget and never outlives this call.
	ctx, cancel := context.WithTimeout(context.Background(), h.budget)
	defer cancel()
	if err := h.readiness.Ping(ctx); err != nil {
		h.log.Warn("readiness check failed",
			slog.String("event", "health.not_ready"),
			slog.String("request_id", middleware.GetRequestID(c)),
			slog.String("error", err.Error()),
		)
		return &APIError{Status: fiber.StatusServiceUnavailable, Code: "not_ready", Message: "Service is not ready"}
	}
	return c.JSON(ReadyResponse{Status: "ready"})
}
