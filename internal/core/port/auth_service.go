package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// AuthService is the inbound port of the auth use cases for the HTTP adapter. Methods are added as
// each use case is built (milestone 4 design section 3); Google sign-in arrives in plan 4c.
type AuthService interface {
	// Authenticate resolves a raw session cookie. An unknown, malformed, expired or revoked token
	// returns domain.ErrUnauthenticated; a failing session store returns dependency_unavailable.
	Authenticate(ctx context.Context, token string) (domain.Principal, error)
	// Register creates an unverified account and sends it a verification code, or tells the owner
	// of an existing account that someone tried to register; the answer is the same either way.
	Register(ctx context.Context, in domain.RegisterInput, meta domain.RequestMeta) (domain.OTPReceipt, error)
}

// EmailVerificationService confirms that the owner of a local account controls its email address.
type EmailVerificationService interface {
	// Request sends a new code to an unverified local account and does nothing for any other email;
	// the answer is the same either way.
	Request(ctx context.Context, email domain.Email, meta domain.RequestMeta) (domain.OTPReceipt, error)
	// Confirm checks the code. A wrong, expired, used or exhausted challenge returns
	// domain.ErrInvalidVerification; a wrong code is counted and audited before that error returns.
	Confirm(ctx context.Context, in domain.VerifyEmailInput, meta domain.RequestMeta) error
}
