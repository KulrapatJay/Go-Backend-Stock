package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// IdentityProvider is an external OpenID Connect provider (Google in phase 1). The adapter owns the
// client ID, client secret and redirect URI, and checks the ID token's signature, issuer, audience
// and expiry before it returns an identity.
type IdentityProvider interface {
	// AuthorizationURL returns the provider's sign-in URL for req. A provider that cannot be reached
	// returns dependency_unavailable.
	AuthorizationURL(ctx context.Context, req domain.AuthorizationRequest) (string, error)
	// Exchange redeems the code and verifies the ID token. A code, verifier or token that the
	// provider or the token check refuses returns domain.ErrIdentityRejected; a provider that cannot
	// be reached returns dependency_unavailable.
	Exchange(ctx context.Context, ex domain.CodeExchange) (domain.VerifiedIdentity, error)
}
