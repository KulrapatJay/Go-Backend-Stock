// Command api is the composition root of the stock portfolio tracker API.
//
// @title           Stock Portfolio Tracker API
// @version         1.0
// @description     Records US stock trades and tracks portfolio cost and P&L. It does not place real orders.
// @description     Milestone 5 exposes Health and the Auth endpoints (email and password, email verification, password reset, Google sign-in and linking); portfolios and transactions are not implemented yet.
// @BasePath        /
// @tag.name        Health
// @tag.description Liveness and readiness probes
// @tag.name        Auth
// @tag.description Registration, email verification, sessions and CSRF tokens. Every POST needs the X-CSRF-Token header from GET /api/v1/auth/csrf and must come from the API's own origin.
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

	app := httpadapter.NewServer(httpadapter.Options{
		Logger:    log,
		Readiness: pool,
		Auth:      auth.options,
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
