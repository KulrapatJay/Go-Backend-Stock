// Package postgres is the secondary adapter for PostgreSQL (local Docker and Supabase).
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// lockTimeout bounds a wait for a row lock (SELECT ... FOR UPDATE on a portfolio). It is below
	// the request deadline, so a waiter fails with a retryable error before the request expires.
	lockTimeout = 3 * time.Second
	// statementTimeout bounds any single statement. Slow statements are cancelled by the server
	// even when the client is gone.
	statementTimeout = 5 * time.Second
)

// NewPool parses databaseURL and creates a lazily-connecting pool limited to maxConns.
// pgx redacts the password in parse errors; TestNewPoolErrorDoesNotLeakPassword guards that.
func NewPool(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	cfg.MaxConns = maxConns
	// Session settings, so every connection has them. A session pooler keeps them; a transaction
	// pooler would not (AGENTS.md forbids it without review).
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		for _, setting := range []struct {
			name string
			d    time.Duration
		}{{"lock_timeout", lockTimeout}, {"statement_timeout", statementTimeout}} {
			if _, err := conn.Exec(ctx, fmt.Sprintf("SET %s = %d", setting.name, setting.d.Milliseconds())); err != nil {
				return fmt.Errorf("set %s: %w", setting.name, err)
			}
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	return pool, nil
}
