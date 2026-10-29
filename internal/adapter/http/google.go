package http

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

// AuthConfig godoc
// @Summary      Get the sign-in options
// @Description  Tells a client whether Google sign-in is configured. When google_enabled is false, the Google endpoints answer 503 feature_disabled.
// @Tags         Auth
// @ID           getAuthConfig
// @Produce      json
// @Success      200  {object}  AuthConfigResponse
// @Router       /api/v1/auth/config [get]
func (h *authHandlers) authConfig(c fiber.Ctx) error {
	return c.JSON(AuthConfigResponse{GoogleEnabled: h.auth.GoogleEnabled()})
}

// GoogleStart godoc
// @Summary      Start signing in with Google
// @Description  Returns the Google URL to open in the same browser, and sets the HttpOnly cookie oidc_binding (__Host-oidc_binding over HTTPS) that must come back with the callback; the flow lasts 10 minutes. Google sends the browser to GET /api/v1/auth/google/callback, which ends at /docs/?google=success|link_required|failed. A Google account whose email already belongs to an account is not linked automatically: sign in to that account and use POST /api/v1/auth/google/link. Needs X-CSRF-Token and the csrf_nonce cookie. Swagger UI and curl cannot complete the Google page; open authorization_url in the browser.
// @Tags         Auth
// @ID           startGoogleSignIn
// @Produce      json
// @Param        X-CSRF-Token  header  string  true  "Token from GET /api/v1/auth/csrf"
// @Success      200  {object}  GoogleStartResponse
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "feature_disabled (Google is not configured) or dependency_unavailable (Google cannot be reached)"
// @Header       200  {string}   Set-Cookie   "oidc_binding=…; Path=/; Expires=…; HttpOnly; SameSite=Lax"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/google/start [post]
func (h *authHandlers) googleStart(c fiber.Ctx) error {
	return h.startGoogle(c, domain.OIDCPurposeLogin)
}

// GoogleLink godoc
// @Summary      Link Google to the signed-in account
// @Description  Like POST /api/v1/auth/google/start, but the flow belongs to the signed-in user: after Google, the callback adds the Google account to this account (/docs/?google=linked) without opening a new session. The callback must come with the same session. Needs the session cookie and an X-CSRF-Token bound to it.
// @Tags         Auth
// @ID           linkGoogleAccount
// @Produce      json
// @Param        X-CSRF-Token  header  string  true  "Token from GET /api/v1/auth/csrf, fetched while signed in"
// @Success      200  {object}  GoogleStartResponse
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "feature_disabled or dependency_unavailable"
// @Header       200  {string}   Set-Cookie   "oidc_binding=…; Path=/; Expires=…; HttpOnly; SameSite=Lax"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/google/link [post]
func (h *authHandlers) googleLink(c fiber.Ctx) error {
	return h.startGoogle(c, domain.OIDCPurposeLink)
}

func (h *authHandlers) startGoogle(c fiber.Ctx, purpose domain.OIDCPurpose) error {
	p, _ := middleware.GetPrincipal(c)
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	r, err := h.auth.StartGoogle(c.Context(), domain.OIDCStart{Purpose: purpose, Principal: p}, meta)
	if err != nil {
		return err
	}
	h.cookies.set(c, h.cookies.bindingName, r.BrowserBinding.Reveal(), r.ExpiresAt)
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(GoogleStartResponse{AuthorizationURL: r.URL})
}

// googleResultPath is where every callback ends; the outcome follows. It is fixed (no return URL from
// the request) and same-origin. It changes when the frontend exists.
const googleResultPath = "/docs/?google="

// GoogleCallback godoc
// @Summary      Come back from Google
// @Description  Google sends the browser here after GET authorization_url. It is not called directly: it needs the state from that URL and the oidc_binding cookie of the browser that started the flow, and it takes no X-CSRF-Token (the state, the binding and the ID token's nonce protect it). It always clears oidc_binding and answers 303 to /docs/?google=success (signed in: the session cookie is set), linked (Google added to the signed-in account), link_required (the email belongs to an account: sign in to it and use POST /api/v1/auth/google/link) or failed (cancelled, expired, replayed, another browser, or refused). Infrastructure failures answer JSON instead.
// @Tags         Auth
// @ID           completeGoogleSignIn
// @Produce      json
// @Param        code   query  string  false  "Authorization code from Google"
// @Param        state  query  string  false  "The state of the flow"
// @Param        error  query  string  false  "Set by Google when the user cancelled"
// @Success      303  "Redirect to /docs/?google=success|linked|link_required|failed"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "feature_disabled (Google is not configured) or dependency_unavailable"
// @Header       303  {string}  Location    "/docs/?google=success"
// @Header       303  {string}  Set-Cookie  "oidc_binding=; Expires=Tue, 10 Nov 2009 23:00:00 GMT — and session=… on success"
// @Router       /api/v1/auth/google/callback [get]
func (h *authHandlers) googleCallback(c fiber.Ctx) error {
	p, _ := middleware.GetPrincipal(c)
	in := domain.OIDCCallback{
		Code:           domain.NewSecretText(strings.Clone(c.Query("code"))),
		State:          strings.Clone(c.Query("state")),
		ProviderError:  strings.Clone(c.Query("error")),
		BrowserBinding: domain.NewSecretText(strings.Clone(c.Cookies(h.cookies.bindingName))),
		Principal:      p,
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	h.cookies.clear(c, h.cookies.bindingName) // the flow ends in this browser whatever happens
	res, err := h.auth.CompleteGoogle(c.Context(), in, meta)
	if err != nil {
		return err
	}
	if res.Outcome == domain.OIDCOutcomeSuccess {
		h.cookies.set(c, h.cookies.sessionName, res.Session.Token.Reveal(), res.Session.ExpiresAt)
		h.cookies.clear(c, h.cookies.nonceName)
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Redirect().Status(fiber.StatusSeeOther).To(googleResultPath + string(res.Outcome))
}
