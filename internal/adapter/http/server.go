package http

import (
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"

	"go-backend-stock/internal/adapter/http/middleware"
)

const (
	bodyLimitBytes  = 64 * 1024
	readTimeout     = 10 * time.Second
	writeTimeout    = 15 * time.Second
	idleTimeout     = 60 * time.Second
	readinessBudget = time.Second
)

// Options are the dependencies of the HTTP server, assembled in cmd/api/main.go.
type Options struct {
	Logger    *slog.Logger
	Readiness ReadinessChecker
}

// NewServer builds the Fiber app with the full middleware chain and every route.
func NewServer(opts Options) *fiber.App {
	app := newApp(opts.Logger)
	registerRoutes(app, opts)
	app.Use(middleware.NotFound())
	return app
}

func newApp(log *slog.Logger) *fiber.App {
	errorHandler := ErrorHandler(log)
	app := fiber.New(fiber.Config{
		AppName:      "stock-api",
		ErrorHandler: errorHandler,
		BodyLimit:    bodyLimitBytes,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
		// StrictRouting keeps /docs and /docs/ distinct so the docs redirect cannot loop.
		StrictRouting: true,
		CaseSensitive: true,
	})
	// Order matters: the request ID exists before anything logs, and AccessLog wraps
	// Recover so a recovered panic is logged with its final 500 status.
	app.Use(middleware.RequestID())
	app.Use(middleware.AccessLog(log, errorHandler))
	app.Use(middleware.Recover(log))
	app.Use(middleware.SecurityHeaders())
	// Fiber's default server error handler runs the Use chain (including NotFound and
	// AccessLog) before it renders the error, so an oversized body would be logged as 404.
	app.Server().ErrorHandler = serverErrorHandler(app, log, errorHandler)
	return app
}

// serverErrorHandler handles errors fasthttp raises before routing (body too large,
// oversized headers, read timeouts). It renders the envelope and then writes the single
// access entry, so the logged status is the final one.
func serverErrorHandler(app *fiber.App, log *slog.Logger, handleError fiber.ErrorHandler) func(*fasthttp.RequestCtx, error) {
	return func(fctx *fasthttp.RequestCtx, err error) {
		start := time.Now()
		c := app.AcquireCtx(fctx)
		defer app.ReleaseCtx(c)

		middleware.MarkRoute(c, "unmatched")
		if id, idErr := middleware.NewRequestID(); idErr == nil {
			middleware.SetRequestID(c, id)
		}
		if herr := handleError(c, transportError(err)); herr != nil {
			_ = c.SendStatus(fiber.StatusInternalServerError)
		}
		middleware.LogAccess(log, c, start)
	}
}

// transportError maps a fasthttp parse/read error to a status without exposing its text.
func transportError(err error) error {
	var netErr net.Error
	switch {
	case errors.Is(err, fasthttp.ErrBodyTooLarge):
		return fiber.ErrRequestEntityTooLarge
	case errors.As(err, new(*fasthttp.ErrSmallBuffer)):
		return fiber.ErrRequestHeaderFieldsTooLarge
	case errors.As(err, &netErr) && netErr.Timeout():
		return fiber.ErrRequestTimeout
	default:
		return fiber.ErrBadRequest
	}
}

func registerRoutes(app *fiber.App, opts Options) {
	health := HealthHandler{readiness: opts.Readiness, log: opts.Logger, budget: readinessBudget}
	app.Get("/healthz", health.Healthz)
	app.Get("/readyz", health.Readyz)
}
