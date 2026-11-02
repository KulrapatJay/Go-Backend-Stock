package port

// Repositories are the repositories a use case reads outside a transaction (one statement each, no
// row locks), plus the rate limiter, whose Admit runs its own transaction. Writes that must commit
// with their audit event go through UnitOfWork instead. Accessors are added when a use case needs
// them.
type Repositories interface {
	Users() UserRepository
	Sessions() SessionRepository
	OTPChallenges() OTPChallengeRepository
	RateLimits() RateLimitRepository
	Identities() IdentityRepository
	OIDCFlows() OIDCFlowRepository // Consume is one conditional UPDATE, so it needs no transaction
}
