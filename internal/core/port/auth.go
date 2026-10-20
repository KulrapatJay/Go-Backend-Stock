package port

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
)

// UserRepository stores accounts. Not-found results return domain.ErrNotFound.
type UserRepository interface {
	ByID(ctx context.Context, id domain.ID) (domain.User, error)
	ByEmail(ctx context.Context, email domain.Email) (domain.User, error)
	// CredentialsByEmail is the only read of password_hash, for login, register and password reset.
	// Every other query leaves the hash out. PasswordHash is "" for Google-only accounts.
	CredentialsByEmail(ctx context.Context, email domain.Email) (domain.UserCredentials, error)
	// Lock reads the user with a row lock; transaction-bound instances only.
	Lock(ctx context.Context, id domain.ID) (domain.User, error)
	// Insert returns domain.ErrIdentityConflict when the email is already registered.
	Insert(ctx context.Context, user domain.User) error
	// SetPassword returns an internal error for an empty hash: Google-only accounts never gain a
	// password this way.
	SetPassword(ctx context.Context, id domain.ID, passwordHash domain.PasswordHash, at time.Time) error
	// MarkEmailVerified is a no-op when the email is already verified.
	MarkEmailVerified(ctx context.Context, id domain.ID, at time.Time) error
}

// SessionRepository stores server-side sessions by token hash. ByTokenHash returns
// domain.ErrNotFound for an unknown hash and returns expired or revoked sessions as stored.
type SessionRepository interface {
	ByTokenHash(ctx context.Context, tokenHash []byte) (domain.Session, error)
	Insert(ctx context.Context, session domain.Session) error
	// Revoke reports false when the session was already revoked, so logout audits only once.
	Revoke(ctx context.Context, id domain.ID, at time.Time, reason domain.RevokeReason) (bool, error)
	// RevokeAll revokes every active session of userID and returns how many it revoked.
	RevokeAll(ctx context.Context, userID domain.ID, at time.Time, reason domain.RevokeReason) (int64, error)
}

// IdentityRepository stores links to external identities, unique per (provider, subject).
// BySubject returns domain.ErrNotFound for an unknown subject; Insert returns
// domain.ErrIdentityConflict when the subject or the user's link to that provider exists.
type IdentityRepository interface {
	BySubject(ctx context.Context, provider domain.IdentityProviderName, subject string) (domain.AuthProvider, error)
	Insert(ctx context.Context, link domain.AuthProvider) error
}

// OIDCFlowRepository stores pending OpenID Connect flows.
type OIDCFlowRepository interface {
	Insert(ctx context.Context, flow domain.OIDCFlow) error
	// Consume atomically marks the flow matching both hashes as consumed if it is unexpired and
	// unconsumed at `at`; a second call for the same flow returns domain.ErrNotFound.
	Consume(ctx context.Context, stateHash, browserBindingHash []byte, at time.Time) (domain.OIDCFlow, error)
}

// OTPChallengeRepository stores OTP challenges. Every method takes the purpose so a challenge
// ID from one flow can never be used in the other. ByID, Lock, RecordFailure and Consume return
// domain.ErrNotFound for an unknown or other-purpose challenge; Consume also does so for a
// challenge that is no longer open.
type OTPChallengeRepository interface {
	ByID(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) (domain.OTPChallenge, error)
	Lock(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) (domain.OTPChallenge, error)
	// InvalidateOpen invalidates userID's open challenges of this purpose, expired ones included.
	InvalidateOpen(ctx context.Context, userID domain.ID, purpose domain.OTPPurpose, at time.Time) error
	Insert(ctx context.Context, challenge domain.OTPChallenge) error
	RecordFailure(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) error
	Consume(ctx context.Context, purpose domain.OTPPurpose, id domain.ID, at time.Time) error
}
