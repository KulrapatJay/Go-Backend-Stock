// Command api is the composition root of the stock portfolio tracker API.
//
// @title           Stock Portfolio Tracker API
// @version         1.0
// @description     Records US stock trades and tracks portfolio cost and P&L. It does not place real orders.
// @description     Milestone 1 exposes only the Health endpoints; every other endpoint is not implemented yet.
// @BasePath        /
// @tag.name        Health
// @tag.description Liveness and readiness probes
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v3"

	apidocs "go-backend-stock/docs/api"
	httpadapter "go-backend-stock/internal/adapter/http"
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

	app := httpadapter.NewServer(httpadapter.Options{
		Logger:    log,
		Readiness: pool,
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
	log.Info("server stopped", "event", "server.stopped")
	return nil
}
