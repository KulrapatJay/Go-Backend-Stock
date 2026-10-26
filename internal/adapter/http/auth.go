package http

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
	"go-backend-stock/internal/core/service"
)

// CSRFTokens issues and checks CSRF tokens (service.CSRFCodec implements it).
type CSRFTokens interface {
	Issue(binding string) (token string, expiresAt time.Time, err error)
	Verify(token, binding string) error
}

// PasswordParser applies the whole password policy (service.PasswordPolicy implements it).
type PasswordParser interface {
	Parse(raw string, email domain.Email) (domain.Password, error)
}

// AuthOptions wires the auth endpoints. A nil Service registers no auth route, so the health and
// docs tests can build the server without it.
type AuthOptions struct {
	Service       port.AuthService
	Verification  port.EmailVerificationService // nil leaves out the email-verification routes
	Passwords     PasswordParser
	CSRF          CSRFTokens
	ClientIP      *ClientIPResolver // nil trusts no proxy header
	PublicOrigin  string            // exact origin of unsafe requests, such as http://localhost:8080
	SecureCookies bool              // PublicOrigin is https: __Host- names and the Secure attribute
}

type authHandlers struct {
	auth         port.AuthService
	verification port.EmailVerificationService
	passwords    PasswordParser
	csrf         CSRFTokens
	clientIP     *ClientIPResolver
	cookies      cookieJar
}

// registerAuthRoutes adds the /api/v1/auth routes. Each route lists its middleware explicitly, in
// the order of phase 1 spec section 5: resolve session → require auth → CSRF/Origin → handler.
func registerAuthRoutes(app *fiber.App, opts AuthOptions) {
	if opts.Service == nil {
		return
	}
	resolver := opts.ClientIP
	if resolver == nil {
		resolver = NewClientIPResolver(nil)
	}
	h := &authHandlers{
		auth: opts.Service, verification: opts.Verification, passwords: opts.Passwords,
		csrf: opts.CSRF, clientIP: resolver, cookies: newCookieJar(opts.SecureCookies),
	}
	session := middleware.ResolveSession(opts.Service, h.cookies.sessionName)

	csrf := middleware.CSRF(opts.CSRF, opts.PublicOrigin, h.cookies.nonceName)

	app.Get("/api/v1/auth/csrf", session, h.csrfToken)
	app.Post("/api/v1/auth/register", session, csrf, h.register)
	if opts.Verification != nil {
		app.Post("/api/v1/auth/email-verification/request", session, csrf, h.requestVerification)
		app.Post("/api/v1/auth/email-verification/confirm", session, csrf, h.confirmEmail)
	}
}

// CSRFToken godoc
// @Summary      Get a CSRF token
// @Description  Returns the token to send in the X-CSRF-Token header of every POST. Signed in, the token is bound to the session; signed out, it is bound to a random nonce that this call keeps in the HttpOnly cookie csrf_nonce (__Host-csrf_nonce over HTTPS). Tokens last one hour. Ask for a new token after login and after logout, because the binding changes. Browsers (and Swagger UI) send the cookie by themselves; curl needs a cookie jar (-b/-c).
// @Tags         Auth
// @ID           getCSRFToken
// @Produce      json
// @Success      200  {object}  CSRFResponse
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable: the session store could not be reached"
// @Header       200  {string}  Set-Cookie  "csrf_nonce=…; Path=/; HttpOnly; SameSite=Lax — only when signed out"
// @Router       /api/v1/auth/csrf [get]
func (h *authHandlers) csrfToken(c fiber.Ctx) error {
	var binding, nonce string
	p, signedIn := middleware.GetPrincipal(c)
	if signedIn {
		binding = service.SessionCSRFBinding(p.SessionID)
	} else {
		nonce = strings.Clone(c.Cookies(h.cookies.nonceName))
		if !middleware.ValidNonce(nonce) {
			fresh, err := service.NewCSRFNonce()
			if err != nil {
				return err
			}
			nonce = fresh
		}
		binding = service.PreLoginCSRFBinding(nonce)
	}
	token, expiresAt, err := h.csrf.Issue(binding)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store") // a token and its nonce cookie must not be cached
	if !signedIn {
		// Renewed on every call so the cookie lives exactly as long as the newest token.
		h.cookies.set(c, h.cookies.nonceName, nonce, expiresAt)
	}
	return c.JSON(CSRFResponse{CSRFToken: token, ExpiresAt: expiresAt.UTC()})
}
