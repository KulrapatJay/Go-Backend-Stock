package http

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

// RequestPasswordReset godoc
// @Summary      Ask for a password reset code
// @Description  Sends a 6-digit code, valid for 10 minutes, to an account that has a password, and invalidates the code sent before. The answer is the same for every email, so it does not reveal whether an account exists; an account that signed up only with Google has no password and gets no code. Each email can ask once a minute and five times an hour, and each IP address 20 times an hour. Needs X-CSRF-Token and the csrf_nonce cookie.
// @Tags         Password Reset
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

// VerifyPasswordReset godoc
// @Summary      Check the reset code and get a reset token
// @Description  First step of the two-step reset. Checks the 6-digit code sent by "ask for a password reset code" without changing the password, so a wrong code shows up before the user picks a new password. A code works once and may be tried wrong 5 times, counted together with the one-step confirm; a wrong, expired, already used, exhausted or unknown code all give the same 400 invalid_reset. Each IP address can try 20 times in 15 minutes, shared with confirm. A right code is spent and turned into reset_token: send it with the new password to POST /api/v1/auth/password-reset/confirm within expires_in seconds (10 minutes). The token works once, only from the same browser (it is tied to the csrf_nonce cookie, or to the session when signed in), and stops working when a new code is requested. Nothing about the account changes yet. Needs X-CSRF-Token and the csrf_nonce cookie.
// @Tags         Password Reset
// @ID           verifyPasswordReset
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string              true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    VerifyResetRequest  true  "challenge_id from the 202 response and the 6 ASCII digits from the email"
// @Success      200  {object}  ResetTokenResponse
// @Header       200  {string}  Cache-Control  "no-store"
// @Failure      400  {object}  ErrorResponse  "invalid_reset, invalid_input or malformed_json"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/password-reset/verify [post]
func (h *authHandlers) verifyPasswordReset(c fiber.Ctx) error {
	var req VerifyResetRequest
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
	binding, err := h.resetBinding(c)
	if err != nil {
		return err
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	receipt, err := h.reset.Verify(c.Context(), domain.VerifyResetInput{ChallengeID: challengeID, OTP: otp, BrowserBinding: binding}, meta)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store") // the body is a credential
	return c.JSON(ResetTokenResponse{ResetToken: receipt.Token.Reveal(), ExpiresIn: int(receipt.ExpiresIn / time.Second)})
}

// ConfirmPasswordReset godoc
// @Summary      Set a new password with the reset token or the reset code
// @Description  Replaces the password. Send either reset_token from POST /api/v1/auth/password-reset/verify (two steps, recommended: a wrong code is reported before the user picks a password) or challenge_id with otp (one step, still supported); sending both, or neither, is 400 invalid_input. A wrong, expired, used, other-browser or superseded reset_token, and a wrong, expired, already used, exhausted or unknown code, all give the same 400 invalid_reset; a code may be tried wrong 5 times. The new password follows the same rules as register; the rule that it must not contain the part of the email before "@" is checked only after the token or code is accepted, so a refusal never reveals anything about the account, and failing that rule spends neither, so another password can be tried. Each IP address can try 20 times in 15 minutes, shared with verify. Success verifies the email, signs the account out on every device, sends a notice email and does not sign in: call POST /api/v1/auth/login next. Needs X-CSRF-Token and the csrf_nonce cookie.
// @Tags         Password Reset
// @ID           confirmPasswordReset
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string                true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    ResetPasswordRequest  true  "new_password with either reset_token, or challenge_id and the 6 ASCII digits from the email"
// @Success      204  "Password changed"
// @Failure      400  {object}  ErrorResponse  "invalid_reset, invalid_input (password rules, details lists the reasons; or both or neither of reset_token and challenge_id/otp) or malformed_json"
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
	withToken, withCode := req.ResetToken != "", req.ChallengeID != "" || req.OTP != ""
	if withToken == withCode {
		return errResetBodyShape
	}
	var challengeID domain.ID
	var otp domain.OTPCode
	var err error
	if withCode {
		if challengeID, err = domain.ParseID("challenge_id", req.ChallengeID); err != nil {
			return err
		}
		if otp, err = domain.ParseOTP(req.OTP); err != nil {
			return err
		}
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
	if withToken {
		binding, err := h.resetBinding(c)
		if err != nil {
			return err
		}
		in := domain.ResetWithTokenInput{Token: domain.NewSecretText(strings.Clone(req.ResetToken)), BrowserBinding: binding, NewPassword: password}
		if err := h.reset.ResetWithToken(c.Context(), in, meta); err != nil {
			return err
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
	if err := h.reset.Reset(c.Context(), domain.ResetPasswordInput{ChallengeID: challengeID, OTP: otp, NewPassword: password}, meta); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

var errResetBodyShape = domain.NewError(domain.CodeInvalidInput, "send either reset_token or challenge_id with otp")

// resetBinding is the CSRF binding the reset token is tied to. The CSRF middleware already
// required one, so a request without it never gets here.
func (h *authHandlers) resetBinding(c fiber.Ctx) (domain.SecretText, error) {
	binding, ok := middleware.CSRFBinding(c, h.cookies.nonceName)
	if !ok {
		return domain.SecretText{}, domain.ErrCSRFFailed
	}
	return domain.NewSecretText(binding), nil
}
