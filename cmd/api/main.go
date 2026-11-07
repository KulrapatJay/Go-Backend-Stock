// Command api is the composition root of the stock portfolio tracker API.
//
// @title           Stock Portfolio Tracker API
// @version         1.0
// @description     Records US stock trades and tracks portfolio cost and P&L. It does not place real orders.
// @description     Phase 1 covers sign-up with email verification (OTP), login and logout with a server-side session cookie, Google sign-in and linking, password reset (OTP), many portfolios per user, recording, editing and deleting buys and sells, and holdings and valuation with delayed prices: sample prices in demo mode (PRICE_PROVIDER=demo) and delayed SIP prices from Alpaca when PRICE_PROVIDER=alpaca. Dividends, splits, mergers and symbol changes are not handled, and results are not tax reports.
// @BasePath        /
// @tag.name        Health
// @tag.description Liveness and readiness probes
// @tag.name        Auth
// @tag.description Registration, email verification, sessions and CSRF tokens. Every POST needs the X-CSRF-Token header from GET /api/v1/auth/csrf and must come from the API's own origin.
// @tag.name        Password Reset
// @tag.description Forgot-password flow with a 6-digit code sent by email: ask for a code, then set a new password with it. The answer to the request is the same for every email. Every POST needs the X-CSRF-Token header from GET /api/v1/auth/csrf and must come from the API's own origin.
// @tag.name        Holdings/Prices
// @tag.description What a portfolio holds and what it is worth, with delayed prices. A price that is missing is null, never zero; realized profit and loss never depends on prices. Read-only, needs the session cookie.
// @tag.name        Portfolios
// @tag.description A user can own many portfolios, all in USD. Every call needs the session cookie; another user's portfolio looks exactly like a missing one (404). Reading returns an ETag with the portfolio version; changing or deleting a portfolio needs it back in If-Match.
// @tag.name        Transactions
// @tag.description Buys and sells a user recorded in one of their portfolios; the API never places orders. Every call needs the session cookie; a portfolio of another user looks exactly like a missing one (404). Quantity, price and fee are decimal strings. Creating a trade needs an Idempotency-Key, so a retry never records it twice; reading returns an ETag with the trade version.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v3"

	apidocs "go-backend-stock/docs/api"
	httpadapter "go-backend-stock/internal/adapter/http"
	"go-backend-stock/internal/adapter/mail"
	"go-backend-stock/internal/adapter/postgres"
	"go-backend-stock/internal/config"
	"go-backend-stock/internal/logging"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logging.New(os.Stdout, cfg.LogLevel, "stock-api")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	//nolint:gosec // DatabaseMaxConns is validated to 1..50 by config.Load.
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, int32(cfg.DatabaseMaxConns))
	if err != nil {
		return err
	}
	defer pool.Close()

	auth, err := buildAuth(cfg, pool, log)
	if err != nil {
		return err
	}
	// The mail queue is closed after HTTP stops (below). This covers every other way out of run.
	mailClosed := false
	defer func() {
		if mailClosed {
			return
		}
		earlyCtx, earlyCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer earlyCancel()
		closeMail(earlyCtx, auth.mail, log)
	}()

	portfolios, err := buildPortfolios(cfg, pool, log)
	if err != nil {
		return err
	}
	transactions, err := buildTransactions(cfg, pool, log)
	if err != nil {
		return err
	}
	valuation, err := buildValuation(cfg, pool, log)
	if err != nil {
		return err
	}

	app := httpadapter.NewServer(httpadapter.Options{
		Logger:       log,
		Readiness:    pool,
		Auth:         auth.options,
		Portfolios:   portfolios,
		Transactions: transactions,
		Valuation:    valuation,
		Docs: httpadapter.DocsOptions{
			Enabled:         cfg.DocsEnabled,
			TryItOutEnabled: cfg.DocsTryItOutEnabled,
			SpecJSON:        apidocs.SwaggerJSON,
			SpecYAML:        apidocs.SwaggerYAML,
		},
	})

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- app.Listen(cfg.HTTPAddr, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	log.Info("server started", "event", "server.start", "addr", cfg.HTTPAddr, "env", string(cfg.Env))

	select {
	case err := <-listenErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	log.Info("shutting down", "event", "server.stop", "timeout", cfg.ShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	// After HTTP, so no handler can queue an email any more; queued emails get the time left.
	mailClosed = true
	closeMail(shutdownCtx, auth.mail, log)
	log.Info("server stopped", "event", "server.stopped")
	return nil
}

// closeMail waits for queued emails until ctx ends and logs, without any address, if some were left.
func closeMail(ctx context.Context, d *mail.Dispatcher, log *slog.Logger) {
	if err := d.Close(ctx); err != nil {
		log.Warn("emails were still queued at the shutdown deadline", "event", "mail.shutdown_incomplete")
	}
}
