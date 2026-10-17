package port

import "context"

// UnitOfWork runs fn inside one database transaction. The transaction commits only if fn
// returns nil, so a business change and its audit event commit or roll back together.
// fn must not start a nested transaction.
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
