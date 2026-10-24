package port

import "context"

// UnitOfWork runs fn inside one database transaction. The transaction commits only if fn
// returns nil, so a business change and its audit event commit or roll back together.
// fn must not start a nested transaction.
//
// A repository error caused by a failed statement ends the transaction: PostgreSQL aborts it,
// so any later statement fails with SQLSTATE 25P02 and Commit rolls back. That includes the
// conflict errors mapped from unique violations (ErrPortfolioNameConflict, ErrIdentityConflict,
// ErrIdempotencyConflict). A service that must record a failure audit after such an error cannot
// do it in the same callback; it returns the error, lets the transaction roll back, and writes
// the audit in a second WithinTransaction call. Repositories avoid the abort where a conflict is
// expected by using INSERT ... ON CONFLICT DO NOTHING and checking the affected row count.
type UnitOfWork interface {
	WithinTransaction(ctx context.Context, fn func(TxRepositories) error) error
}

// TxRepositories are the repositories bound to the current transaction. Lock* methods are only
// valid on these instances.
type TxRepositories interface {
	Users() UserRepository
	Sessions() SessionRepository
	Identities() IdentityRepository
	OTPChallenges() OTPChallengeRepository // otp_challenges for both purposes
	Portfolios() PortfolioRepository
	Transactions() TransactionRepository
	Idempotency() IdempotencyRepository
	Instruments() InstrumentRepository
	Audit() AuditRepository
}
