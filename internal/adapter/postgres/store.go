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

// Store owns the connection pool, builds repositories and is the port.UnitOfWork.
type Store struct {
	pool *pgxpool.Pool
}

var (
	_ port.UnitOfWork     = (*Store)(nil)
	_ port.TxRepositories = (*txRepositories)(nil)
)

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

// RateLimits returns the rate-limit repository. Admit runs its own transaction.
func (s *Store) RateLimits() port.RateLimitRepository { return &RateLimitRepository{store: s} }

// Portfolios returns the portfolio repository over the pool.
func (s *Store) Portfolios() port.PortfolioRepository {
	return &PortfolioRepository{q: sqlc.New(s.pool)}
}

// Transactions returns the transaction repository over the pool.
func (s *Store) Transactions() port.TransactionRepository {
	return &TransactionRepository{q: sqlc.New(s.pool)}
}

// Instruments returns the instrument repository over the pool.
func (s *Store) Instruments() port.InstrumentRepository {
	return &InstrumentRepository{q: sqlc.New(s.pool)}
}

// txRepositories are the repositories bound to one database transaction (port.TxRepositories).
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

// Portfolios returns the portfolio repository bound to this transaction.
func (r *txRepositories) Portfolios() port.PortfolioRepository {
	return &PortfolioRepository{q: r.q, inTx: true}
}

// Transactions returns the transaction repository bound to this transaction.
func (r *txRepositories) Transactions() port.TransactionRepository {
	return &TransactionRepository{q: r.q}
}

// Idempotency returns the idempotency repository bound to this transaction. Records are only
// read and written under the portfolio lock, so there is no pool accessor.
func (r *txRepositories) Idempotency() port.IdempotencyRepository {
	return &IdempotencyRepository{q: r.q}
}

// Instruments returns the instrument repository bound to this transaction.
func (r *txRepositories) Instruments() port.InstrumentRepository {
	return &InstrumentRepository{q: r.q}
}

// Audit returns the audit repository bound to this transaction.
func (r *txRepositories) Audit() port.AuditRepository {
	return &AuditRepository{q: r.q}
}

// WithinTransaction runs fn in one transaction (see withinTx and port.UnitOfWork).
func (s *Store) WithinTransaction(ctx context.Context, fn func(port.TxRepositories) error) error {
	return s.withinTx(ctx, func(r *txRepositories) error { return fn(r) })
}

// withinTx runs fn in one READ COMMITTED transaction (row locks serialize writes, ADR-0009).
// The transaction commits only when fn returns nil; an error, a panic or runtime.Goexit rolls it
// back, so a business change and its audit event are never committed apart.
func (s *Store) withinTx(ctx context.Context, fn func(*txRepositories) error) (err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("%w: begin transaction: %w", domain.ErrInternal, err)
	}
	// Roll back with a context that outlives a cancelled request.
	rollback := func() error { return tx.Rollback(context.WithoutCancel(ctx)) }
	returned := false
	defer func() {
		// fn panicked or left the goroutine (runtime.Goexit, as t.Fatal does): end the transaction
		// so its connection goes back to the pool. A panic keeps unwinding after this.
		if !returned {
			_ = rollback()
		}
	}()

	err = fn(&txRepositories{q: sqlc.New(tx)})
	returned = true
	if err != nil {
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
