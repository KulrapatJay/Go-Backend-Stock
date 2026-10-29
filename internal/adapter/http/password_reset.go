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

// ConfirmPasswordReset godoc
// @Summary      Set a new password with the reset code
// @Description  Checks the 6-digit code sent by "ask for a password reset code" and replaces the password. A code works once and may be tried wrong 5 times; a wrong, expired, already used, exhausted or unknown code all give the same 400 invalid_reset. The new password follows the same rules as register; the rule that it must not contain the part of the email before "@" is checked only after the code is right, so a wrong code never reveals anything about the account. Success verifies the email, signs the account out on every device, sends a notice email and does not sign in: call POST /api/v1/auth/login next. Needs X-CSRF-Token and the csrf_nonce cookie.
// @Tags         Auth
// @ID           confirmPasswordReset
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string                true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    ResetPasswordRequest  true  "challenge_id from the 202 response, the 6 ASCII digits from the email and the new password"
// @Success      204  "Password changed"
// @Failure      400  {object}  ErrorResponse  "invalid_reset, invalid_input (password rules, details lists the reasons) or malformed_json"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable or server_busy"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Header       503  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/password-reset/confirm [post]
func (h *authHandlers) confirmPasswordReset(c fiber.Ctx) error {
	var req ResetPasswordRequest
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
	// Every rule but the email one: the account is not known yet, and checking its email before
	// the code would let a guessed challenge ID probe the account (the service checks it later).
	password, err := h.passwords.Parse(req.NewPassword, domain.Email{})
	if err != nil {
		return err
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	if err := h.reset.Reset(c.Context(), domain.ResetPasswordInput{ChallengeID: challengeID, OTP: otp, NewPassword: password}, meta); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
