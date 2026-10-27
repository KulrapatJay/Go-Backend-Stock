package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

// Login godoc
// @Summary      Sign in with an email and a password
// @Description  Opens a session and sets it in the HttpOnly cookie session (__Host-session over HTTPS; SameSite=Lax, Path=/); the body only describes it. An unknown email, a wrong password and an account that signs in only with Google all give the same 401. The right password of an account whose email is not verified gives 403 email_not_verified. The pre-login csrf_nonce cookie is cleared: call GET /api/v1/auth/csrf again for a token bound to the new session. Needs X-CSRF-Token.
// @Tags         Auth
// @ID           login
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string        true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    LoginRequest  true  "Email and password"
// @Success      200  {object}  SessionResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input or malformed_json"
// @Failure      401  {object}  ErrorResponse  "invalid_credentials"
// @Failure      403  {object}  ErrorResponse  "email_not_verified or csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "server_busy (wait Retry-After seconds) or dependency_unavailable"
// @Header       200  {string}   Set-Cookie   "session=…; Path=/; Expires=…; HttpOnly; SameSite=Lax"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Header       503  {integer}  Retry-After  "Seconds to wait (server_busy)"
// @Router       /api/v1/auth/login [post]
func (h *authHandlers) login(c fiber.Ctx) error {
	var req LoginRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	email, err := domain.ParseEmail(req.Email)
	if err != nil {
		return err
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	grant, err := h.auth.Login(c.Context(), domain.LoginInput{Email: email, Password: domain.NewSecretText(req.Password)}, meta)
	if err != nil {
		return err
	}
	h.cookies.set(c, h.cookies.sessionName, grant.Token.Reveal(), grant.ExpiresAt)
	h.cookies.clear(c, h.cookies.nonceName)
	return c.JSON(SessionResponse{User: newUserResponse(grant.User), ExpiresAt: grant.ExpiresAt.UTC()})
}

// Me godoc
// @Summary      Get the signed-in account
// @Description  Needs the session cookie from POST /api/v1/auth/login.
// @Tags         Auth
// @ID           getCurrentUser
// @Produce      json
// @Success      200  {object}  UserResponse
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable: the session store could not be reached"
// @Router       /api/v1/auth/me [get]
func (h *authHandlers) me(c fiber.Ctx) error {
	p, _ := middleware.GetPrincipal(c)
	user, err := h.auth.CurrentUser(c.Context(), p)
	if err != nil {
		return err
	}
	return c.JSON(newUserResponse(user))
}

// Logout godoc
// @Summary      Sign out
// @Description  Revokes the current session on the server and clears the session cookie. Without an active session (none, expired or already revoked) it only clears the cookie and still answers 204. Needs X-CSRF-Token: signed in, a token bound to the session; otherwise one bound to the csrf_nonce cookie.
// @Tags         Auth
// @ID           logout
// @Produce      json
// @Param        X-CSRF-Token  header  string  true  "Token from GET /api/v1/auth/csrf"
// @Success      204  "Signed out"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable"
// @Header       204  {string}  Set-Cookie  "session=; Path=/; Expires=Tue, 10 Nov 2009 23:00:00 GMT; HttpOnly; SameSite=Lax"
// @Router       /api/v1/auth/logout [post]
func (h *authHandlers) logout(c fiber.Ctx) error {
	p, _ := middleware.GetPrincipal(c)
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	if err := h.auth.Logout(c.Context(), p, meta); err != nil {
		return err // the session may still be valid: keep the cookie so the browser does not look signed out
	}
	h.cookies.clear(c, h.cookies.sessionName) // also for a caller without a session: still 204
	return c.SendStatus(fiber.StatusNoContent)
}
