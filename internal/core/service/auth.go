package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// AuthDeps are the dependencies of NewAuth, assembled in cmd/api.
type AuthDeps struct {
	Store          port.Repositories // reads outside a transaction and the rate limiter
	UnitOfWork     port.UnitOfWork   // writes that commit with their audit event
	Clock          port.Clock
	Hasher         *PasswordHasher
	OTP            *OTPCodec
	Limits         *RateLimitPolicy
	Pacer          *Pacer
	Mailer         port.Mailer
	Logger         *slog.Logger
	SessionTTL     time.Duration
	OTPMaxAttempts int
	Identity       port.IdentityProvider // nil turns Google sign-in off
	OIDC           *OIDCCodec            // required when Identity is set
}

// Auth implements the auth use cases (port.AuthService and, from plan 4b task 10,
// port.EmailVerificationService). Safe for concurrent use.
type Auth struct {
	store          port.Repositories
	uow            port.UnitOfWork
	clock          port.Clock
	hasher         *PasswordHasher
	otp            *OTPCodec
	limits         *RateLimitPolicy
	pacer          *Pacer
	mailer         port.Mailer
	log            *slog.Logger
	sessionTTL     time.Duration
	otpMaxAttempts int
	identity       port.IdentityProvider
	oidc           *OIDCCodec
}

var _ port.AuthService = (*Auth)(nil)

// NewAuth checks that every dependency is present.
func NewAuth(d AuthDeps) (*Auth, error) {
	switch {
	case d.Store == nil, d.UnitOfWork == nil, d.Clock == nil, d.Hasher == nil, d.OTP == nil,
		d.Limits == nil, d.Pacer == nil, d.Mailer == nil, d.Logger == nil:
		return nil, errors.New("auth service is missing a dependency")
	case d.SessionTTL <= 0:
		return nil, errors.New("session lifetime must be positive")
	case d.OTPMaxAttempts < 1 || d.OTPMaxAttempts > domain.MaxOTPFailedAttemptsCeiling:
		return nil, fmt.Errorf("OTP attempts must be between 1 and %d", domain.MaxOTPFailedAttemptsCeiling)
	case d.Identity != nil && d.OIDC == nil:
		return nil, errors.New("google sign-in needs the OIDC codec")
	}
	return &Auth{
		store: d.Store, uow: d.UnitOfWork, clock: d.Clock, hasher: d.Hasher, otp: d.OTP,
		limits: d.Limits, pacer: d.Pacer, mailer: d.Mailer, log: d.Logger,
		sessionTTL: d.SessionTTL, otpMaxAttempts: d.OTPMaxAttempts,
		identity: d.Identity, oidc: d.OIDC,
	}, nil
}

// Authenticate implements port.AuthService.
func (a *Auth) Authenticate(ctx context.Context, token string) (domain.Principal, error) {
	hash, ok := HashSessionToken(token)
	if !ok {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	s, err := a.store.Sessions().ByTokenHash(ctx, hash)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.Principal{}, domain.ErrUnauthenticated
	case err != nil:
		return domain.Principal{}, sessionStoreUnavailable(err)
	}
	if s.RevokedAt != nil || !NowForStorage(a.clock).Before(s.ExpiresAt) {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	return domain.Principal{UserID: s.UserID, SessionID: s.ID}, nil
}

// sessionStoreUnavailable reports a failing session lookup as 503, not as "not signed in": a
// database outage must not look like a logout (phase 1 spec section 10).
func sessionStoreUnavailable(err error) error {
	return fmt.Errorf("%w: %w", domain.NewError(domain.CodeDependencyUnavailable, "session store is unavailable"), err)
}
