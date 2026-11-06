// Command maintenance is the composition root of the retention cleanup job. It runs once, deletes
// what outlived its retention (audit events, expired sessions, OTP challenges, OpenID Connect flows,
// idempotency records and rate-limit state) as the app_maintenance database role, logs what it
// deleted and exits. It has no HTTP server and writes no audit event: its role may not.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-backend-stock/internal/adapter/postgres"
	"go-backend-stock/internal/config"
	"go-backend-stock/internal/core/service"
	"go-backend-stock/internal/logging"
)

// batchSize is how many rows one DELETE statement removes (phase 1 spec, audit retention).
const batchSize = 1000

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadMaintenance(os.Getenv)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logging.New(os.Stdout, cfg.LogLevel, "stock-maintenance")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	// One statement at a time, so two connections are plenty.
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	retention := service.NewRetention(postgres.NewRetentionRepository(pool), systemClock{}, service.RetentionPolicy{
		AuditRetention: cfg.AuditRetention,
		BatchSize:      batchSize,
	})
	started := time.Now()
	report, err := retention.Run(ctx)
	duration := time.Since(started).Milliseconds()
	if err != nil {
		// The error names the table and carries the database error, never a connection string.
		log.Error("retention failed", append(reportAttrs(report), "event", "maintenance.retention_failed",
			"error", err.Error(), "duration_ms", duration)...)
		return fmt.Errorf("retention: %w", err)
	}
	log.Info("retention finished", append(reportAttrs(report), "event", "maintenance.retention_finished",
		"duration_ms", duration)...)
	return nil
}

// reportAttrs lists the rows deleted from each table, by table name.
func reportAttrs(r service.RetentionReport) []any {
	return []any{
		slog.Int64("audit_events", r.AuditEvents),
		slog.Int64("idempotency_keys", r.IdempotencyKeys),
		slog.Int64("sessions", r.Sessions),
		slog.Int64("otp_challenges", r.OTPChallenges),
		slog.Int64("oauth_flows", r.OIDCFlows),
		slog.Int64("rate_limit_events", r.RateLimitEvents),
		slog.Int64("rate_limit_keys", r.RateLimitKeys),
	}
}
