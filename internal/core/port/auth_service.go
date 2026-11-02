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
	// Login checks the password and opens a session. Every failure that must not reveal the account
	// returns domain.ErrInvalidCredentials; the right password of an unverified account returns
	// domain.ErrEmailNotVerified. Failures are audited before the error returns.
	Login(ctx context.Context, in domain.LoginInput, meta domain.RequestMeta) (domain.SessionGrant, error)
	// CurrentUser returns the account of p; an account that no longer exists is ErrUnauthenticated.
	CurrentUser(ctx context.Context, p domain.Principal) (domain.User, error)
	// Logout revokes p's session. A zero p (no active session) does nothing; a session that was
	// revoked meanwhile is not audited again.
	Logout(ctx context.Context, p domain.Principal, meta domain.RequestMeta) error
	// GoogleEnabled reports whether Google sign-in is configured.
	GoogleEnabled() bool
	// StartGoogle begins a Google login or, for a signed-in caller, a link. It returns the provider's
	// URL and the browser binding for the cookie. Without Google it returns domain.ErrFeatureDisabled.
	StartGoogle(ctx context.Context, in domain.OIDCStart, meta domain.RequestMeta) (domain.OIDCRedirect, error)
	// CompleteGoogle ends a flow at the callback. Every refusal is an OIDCResult with outcome
	// failed (audited when a flow was found); only infrastructure failures return an error.
	CompleteGoogle(ctx context.Context, in domain.OIDCCallback, meta domain.RequestMeta) (domain.OIDCResult, error)
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

// PasswordResetService lets the owner of a local account choose a new password with a code sent to
// its email.
type PasswordResetService interface {
	// Request sends a reset code to a local account with a password and does nothing for any other email;
	// the answer is the same either way.
	Request(ctx context.Context, email domain.Email, meta domain.RequestMeta) (domain.OTPReceipt, error)
	// Reset checks the code, replaces the password and revokes every session of the account in one
	// transaction. A wrong, expired, used or exhausted challenge returns domain.ErrInvalidReset; a
	// wrong code is counted and audited before that error returns.
	Reset(ctx context.Context, in domain.ResetPasswordInput, meta domain.RequestMeta) error
}
