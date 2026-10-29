package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

// RequestPasswordReset godoc
// @Summary      Ask for a password reset code
// @Description  Sends a 6-digit code, valid for 10 minutes, to an account that has a password, and invalidates the code sent before. The answer is the same for every email, so it does not reveal whether an account exists; an account that signed up only with Google has no password and gets no code. Each email can ask once a minute and five times an hour. Needs X-CSRF-Token and the csrf_nonce cookie.
// @Tags         Auth
// @ID           requestPasswordReset
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string        true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    EmailRequest  true  "The email to send a code to"
// @Success      202  {object}  OTPAcceptedResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input or malformed_json"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/password-reset/request [post]
func (h *authHandlers) requestPasswordReset(c fiber.Ctx) error {
	var req EmailRequest
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
	receipt, err := h.reset.Request(c.Context(), email, meta)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(newOTPAccepted(receipt))
}
