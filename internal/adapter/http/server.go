package http

import (
	"errors"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
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
	// requestTimeout is the deadline of a request's context. It is above the database lock_timeout
	// (3s) and statement_timeout (5s), so a database wait ends first with its own retryable error.
	requestTimeout = 12 * time.Second
)

// Options are the dependencies of the HTTP server, assembled in cmd/api/main.go.
type Options struct {
	Logger     *slog.Logger
	Readiness  ReadinessChecker
	Docs       DocsOptions
	Auth       AuthOptions
	Portfolios PortfolioOptions
	// Transactions serves the trades of a portfolio and Valuation its holdings and value; both need
	// Auth to resolve the session.
	Transactions TransactionOptions
	Valuation    ValuationOptions
}

// NewServer builds the Fiber app with the full middleware chain and every route.
func NewServer(opts Options) *fiber.App {
	app := newApp(opts.Logger)
	registerRoutes(app, opts)
	app.Use(middleware.NotFound(routeMethods(app)))
	return app
}

// routeMethods returns, for a request path, the methods of the registered routes that match it.
// A route segment starting with ':' matches any non-empty segment of the same position, so
// /api/v1/portfolios/<id> is found through /api/v1/portfolios/:portfolio_id. Routes with a
// wildcard ('*' or '+') are left out. The table is built on first use, when every route has been
// registered; methods are sorted and unique.
func routeMethods(app *fiber.App) func(path string) []string {
	type route struct {
		segments []string
		methods  []string
	}
	var once sync.Once
	var routes []*route
	return func(path string) []string {
		once.Do(func() {
			byPath := map[string]*route{}
			for _, r := range app.GetRoutes(true) {
				if strings.ContainsAny(r.Path, "*+") {
					continue
				}
				entry := byPath[r.Path]
				if entry == nil {
					entry = &route{segments: strings.Split(r.Path, "/")}
					byPath[r.Path] = entry
					routes = append(routes, entry)
				}
				if !slices.Contains(entry.methods, r.Method) {
					entry.methods = append(entry.methods, r.Method)
				}
			}
		})
		segments := strings.Split(path, "/")
		var methods []string
		for _, r := range routes {
			if matchSegments(r.segments, segments) {
				for _, m := range r.methods {
					if !slices.Contains(methods, m) {
						methods = append(methods, m)
					}
				}
			}
		}
		slices.Sort(methods)
		return methods
	}
}

// matchSegments reports whether a concrete path matches a route pattern, segment by segment.
func matchSegments(pattern, path []string) bool {
	if len(pattern) != len(path) {
		return false
	}
	for i, seg := range pattern {
		if strings.HasPrefix(seg, ":") {
			if path[i] == "" {
				return false
			}
			continue
		}
		if seg != path[i] {
			return false
		}
	}
	return true
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
	app.Use(middleware.RequestTimeout(requestTimeout))
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
	getAndHead(app, "/healthz", health.Healthz)
	getAndHead(app, "/readyz", health.Readyz)
	registerDocs(app, opts.Docs)
	if opts.Auth.Service == nil {
		if opts.Portfolios.Service != nil || opts.Transactions.Service != nil || opts.Valuation.Service != nil {
			panic("http: portfolio, transaction and valuation routes need Options.Auth.Service to resolve the session")
		}
		return
	}
	chain := newAuthChain(opts.Auth)
	registerAuthRoutes(app, opts.Auth, chain)
	registerPortfolioRoutes(app, opts.Portfolios, chain)
	registerTransactionRoutes(app, opts.Transactions, chain)
	registerValuationRoutes(app, opts.Valuation, chain)
}

// getAndHead registers handler for GET and HEAD. Fiber's automatic HEAD routes are added at
// startup, after the catch-all NotFound middleware, so they would never match; fasthttp drops
// the body of HEAD responses itself.
func getAndHead(app *fiber.App, path string, handler any, handlers ...any) {
	app.Get(path, handler, handlers...)
	app.Head(path, handler, handlers...)
}
