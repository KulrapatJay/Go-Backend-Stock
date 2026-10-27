package middleware

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

// HeaderCSRFToken carries the token from GET /api/v1/auth/csrf on every unsafe request.
const HeaderCSRFToken = "X-CSRF-Token" //nolint:gosec // G101 false positive: a header name, not a credential.

// CSRFVerifier checks a CSRF token against its binding (service.CSRFCodec implements it).
type CSRFVerifier interface {
	Verify(token, binding string) error
}

// CSRF protects every request that is not GET, HEAD or OPTIONS: the Origin (or, without one, the
// origin of the Referer) must equal publicOrigin exactly, and X-CSRF-Token must be bound to the
// current session or, before login, to the nonce in the nonceCookie cookie. Every failure is the
// same 403 csrf_failed. Run it after ResolveSession, so a signed-in request uses the session binding.
func CSRF(verifier CSRFVerifier, publicOrigin, nonceCookie string) fiber.Handler {
	return func(c fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}
		if !fromOrigin(c, publicOrigin) {
			return domain.ErrCSRFFailed
		}
		binding, ok := csrfBinding(c, nonceCookie)
		if !ok {
			return domain.ErrCSRFFailed
		}
		if err := verifier.Verify(strings.Clone(c.Get(HeaderCSRFToken)), binding); err != nil {
			return domain.ErrCSRFFailed
		}
		return c.Next()
	}
}

// fromOrigin reports whether the request comes from want. Browsers send Origin on POST; a client
// without one may send a Referer instead; a request with neither is refused.
func fromOrigin(c fiber.Ctx, want string) bool {
	origin := c.Get(fiber.HeaderOrigin)
	if origin == "" {
		referer := c.Get(fiber.HeaderReferer)
		if referer == "" {
			return false
		}
		u, err := url.Parse(referer)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return false
		}
		origin = u.Scheme + "://" + u.Host
	}
	return origin == want
}

func csrfBinding(c fiber.Ctx, nonceCookie string) (string, bool) {
	if p, ok := GetPrincipal(c); ok {
		return service.SessionCSRFBinding(p.SessionID), true
	}
	nonce := strings.Clone(c.Cookies(nonceCookie))
	if !ValidNonce(nonce) {
		return "", false
	}
	return service.PreLoginCSRFBinding(nonce), true
}

// ValidNonce reports whether s has the shape service.NewCSRFNonce produces: 32 bytes as 43
// unpadded base64url characters. A cookie of any other shape is treated as missing.
func ValidNonce(s string) bool {
	if len(s) != 43 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(raw) == 32
}
