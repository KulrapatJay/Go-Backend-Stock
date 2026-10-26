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
}
