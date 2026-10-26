package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

// Register godoc
// @Summary      Register with an email and a password
// @Description  Creates an unverified account and emails a 6-digit verification code. The answer is the same whether or not the email already has an account: the owner of an existing account gets an email notice instead and nothing changes. The response takes at least about half a second on purpose. Needs the pre-login CSRF flow: call GET /api/v1/auth/csrf first, send its token in X-CSRF-Token, and send the csrf_nonce cookie (a browser does this itself). Then confirm the code with POST /api/v1/auth/email-verification/confirm.
// @Tags         Auth
// @ID           register
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string           true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    RegisterRequest  true  "Email, and a password of 6-32 printable ASCII characters without spaces, with a lower-case letter, an upper-case letter and a special character"
// @Success      202  {object}  OTPAcceptedResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input (details lists every broken password rule) or malformed_json"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "server_busy (wait Retry-After seconds) or dependency_unavailable"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Header       503  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/register [post]
func (h *authHandlers) register(c fiber.Ctx) error {
	var req RegisterRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	email, err := domain.ParseEmail(req.Email)
	if err != nil {
		return err
	}
	password, err := h.passwords.Parse(req.Password, email)
	if err != nil {
		return err
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	receipt, err := h.auth.Register(c.Context(), domain.RegisterInput{Email: email, Password: password}, meta)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(newOTPAccepted(receipt))
}
