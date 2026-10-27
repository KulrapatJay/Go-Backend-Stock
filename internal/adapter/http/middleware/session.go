package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

// Authenticator resolves a raw session cookie (port.AuthService implements it).
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (domain.Principal, error)
}

type principalKey struct{}

// ResolveSession stores the caller in the request when the session cookie names an active session.
// No cookie, or an unknown, expired or revoked session, leaves the request anonymous; a failing
// session store is an error (503), never a silent logout.
func ResolveSession(auth Authenticator, cookieName string) fiber.Handler {
	return func(c fiber.Ctx) error {
		token := strings.Clone(c.Cookies(cookieName))
		if token == "" {
			return c.Next()
		}
		p, err := auth.Authenticate(c.Context(), token)
		switch {
		case errors.Is(err, domain.ErrUnauthenticated):
			return c.Next()
		case err != nil:
			return err
		}
		c.Locals(principalKey{}, p)
		return c.Next()
	}
}

// RequireAuth refuses an anonymous request with 401 unauthenticated. Ownership of data is checked
// again by the use case and its queries; this only stops requests that have no caller at all.
func RequireAuth() fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, ok := GetPrincipal(c); !ok {
			return domain.ErrUnauthenticated
		}
		return c.Next()
	}
}

// GetPrincipal returns the caller ResolveSession found, if any. The value is a copy that does not
// refer to the request.
func GetPrincipal(c fiber.Ctx) (domain.Principal, bool) {
	p, ok := c.Locals(principalKey{}).(domain.Principal)
	return p, ok && !p.IsZero()
}
