package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

// RequestVerification godoc
// @Summary      Send a new verification code
// @Description  Sends a new 6-digit code to an account that registered with a password and is not verified yet, and invalidates its previous code. The answer is the same for every email, so it does not reveal whether an account exists; register and this endpoint share one quota per email (one code a minute, five an hour). Needs X-CSRF-Token and the csrf_nonce cookie.
// @Tags         Auth
// @ID           requestEmailVerification
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string        true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    EmailRequest  true  "The email to send a code to"
// @Success      202  {object}  OTPAcceptedResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input or malformed_json"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/email-verification/request [post]
func (h *authHandlers) requestVerification(c fiber.Ctx) error {
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
	receipt, err := h.verification.Request(c.Context(), email, meta)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(newOTPAccepted(receipt))
}

// ConfirmEmail godoc
// @Summary      Confirm an email address
// @Description  Checks the 6-digit code sent by register or by "send a new code". A wrong, expired, already used or exhausted code (5 wrong tries) gives the same 400 invalid_verification. Success verifies the email but does not sign in: call POST /api/v1/auth/login next. Needs X-CSRF-Token and the csrf_nonce cookie.
// @Tags         Auth
// @ID           confirmEmailVerification
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string               true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    ConfirmEmailRequest  true  "challenge_id from the 202 response and the 6 ASCII digits from the email"
// @Success      204  "Email verified"
// @Failure      400  {object}  ErrorResponse  "invalid_verification, invalid_input or malformed_json"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/email-verification/confirm [post]
func (h *authHandlers) confirmEmail(c fiber.Ctx) error {
	var req ConfirmEmailRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	challengeID, err := domain.ParseID("challenge_id", req.ChallengeID)
	if err != nil {
		return err
	}
	otp, err := domain.ParseOTP(req.OTP)
	if err != nil {
		return err
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	if err := h.verification.Confirm(c.Context(), domain.VerifyEmailInput{ChallengeID: challengeID, OTP: otp}, meta); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
