package domain

import "time"

// RegisterInput is a registration request whose email is normalized and whose password passed the
// whole policy (service.PasswordPolicy).
type RegisterInput struct {
	Email    Email
	Password Password
}

// LoginInput is a login request. The password is only wrapped: login checks its length and nothing
// else, so an account whose password predates today's rules can still sign in (phase 1 spec
// section 10).
type LoginInput struct {
	Email    Email
	Password SecretText
}

// VerifyEmailInput confirms an email address with the code that was sent to it.
type VerifyEmailInput struct {
	ChallengeID ID
	OTP         OTPCode
}

// ResetPasswordInput finishes a password reset: the code that was sent to the account's email and
// the new password, which passed every password rule except the one about the email, which Reset
// checks after the code is verified.
type ResetPasswordInput struct {
	ChallengeID ID
	OTP         OTPCode
	NewPassword Password
}

// VerifyResetInput is the first step of the two-step password reset (ADR-0022): the code that was
// sent to the account's email, and the CSRF binding of the caller's browser, which the reset token
// is tied to.
type VerifyResetInput struct {
	ChallengeID    ID
	OTP            OTPCode
	BrowserBinding SecretText
}

// ResetGrantReceipt answers a verified code: the reset token for the second step and how long it
// lasts. The token prints as [REDACTED].
type ResetGrantReceipt struct {
	Token     SecretText
	ExpiresIn time.Duration
}

// ResetWithTokenInput finishes a two-step password reset. The new password passed every rule but
// the one about the email, which the service checks once the token is accepted; BrowserBinding
// must be the binding the token was issued to.
type ResetWithTokenInput struct {
	Token          SecretText
	BrowserBinding SecretText
	NewPassword    Password
}
