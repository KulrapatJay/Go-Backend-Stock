package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
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

// errLockOutsideTx is returned by Lock* on a repository that is not bound to a transaction: a row
// lock outside a transaction is released at once and protects nothing (phase 1 spec section 8).
var errLockOutsideTx = domain.NewError(domain.CodeInternal, "row lock requires a transaction")

// Repositories that read outside a transaction. They use the pool, so each call is its own
// statement; Lock* methods refuse to run on them.

// Users returns the user repository over the pool.
func (s *Store) Users() port.UserRepository { return &UserRepository{q: sqlc.New(s.pool)} }

// Sessions returns the session repository over the pool.
func (s *Store) Sessions() port.SessionRepository { return &SessionRepository{q: sqlc.New(s.pool)} }

// Identities returns the identity repository over the pool.
func (s *Store) Identities() port.IdentityRepository {
	return &IdentityRepository{q: sqlc.New(s.pool)}
}

// OTPChallenges returns the OTP challenge repository over the pool.
func (s *Store) OTPChallenges() port.OTPChallengeRepository {
	return &OTPChallengeRepository{q: sqlc.New(s.pool)}
}

// OIDCFlows returns the OIDC flow repository. Its Consume is a single statement, so it needs no
// surrounding transaction.
func (s *Store) OIDCFlows() port.OIDCFlowRepository {
	return &OIDCFlowRepository{q: sqlc.New(s.pool)}
}

// txRepositories are the repositories bound to one database transaction. Plan 3c adds the
// portfolio-side accessors until it satisfies port.TxRepositories.
type txRepositories struct {
	q *sqlc.Queries
}

// Users returns the user repository bound to this transaction.
func (r *txRepositories) Users() port.UserRepository { return &UserRepository{q: r.q, inTx: true} }

// Sessions returns the session repository bound to this transaction.
func (r *txRepositories) Sessions() port.SessionRepository { return &SessionRepository{q: r.q} }

// Identities returns the identity repository bound to this transaction.
func (r *txRepositories) Identities() port.IdentityRepository { return &IdentityRepository{q: r.q} }

// OTPChallenges returns the OTP challenge repository bound to this transaction.
func (r *txRepositories) OTPChallenges() port.OTPChallengeRepository {
	return &OTPChallengeRepository{q: r.q, inTx: true}
}

// Audit returns the audit repository bound to this transaction.
func (r *txRepositories) Audit() port.AuditRepository {
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
