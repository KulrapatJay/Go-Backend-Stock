package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
)

// Store owns the connection pool and builds repositories. It becomes the port.UnitOfWork once
// every repository exists (milestone 3c); until then transactions go through withinTx.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// txRepositories are the repositories bound to one database transaction. Later plans add one
// accessor per repository until it satisfies port.TxRepositories.
type txRepositories struct {
	q *sqlc.Queries
}

// Audit returns the audit repository bound to this transaction.
func (r *txRepositories) Audit() *AuditRepository {
	return &AuditRepository{q: r.q}
}

// withinTx runs fn in one READ COMMITTED transaction (row locks serialize writes, ADR-0009).
// The transaction commits only when fn returns nil; an error or a panic rolls it back, so a
// business change and its audit event are never committed apart.
func (s *Store) withinTx(ctx context.Context, fn func(*txRepositories) error) (err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("%w: begin transaction: %w", domain.ErrInternal, err)
	}
	// Roll back with a context that outlives a cancelled request.
	rollback := func() error { return tx.Rollback(context.WithoutCancel(ctx)) }
	defer func() {
		if p := recover(); p != nil {
			_ = rollback()
			panic(p)
		}
	}()

	if err := fn(&txRepositories{q: sqlc.New(tx)}); err != nil {
		if rbErr := rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit: %w", domain.ErrInternal, err)
	}
	return nil
}
