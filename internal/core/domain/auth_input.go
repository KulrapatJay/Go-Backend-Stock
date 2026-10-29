package domain

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
// the new password, which passed the whole policy (service.PasswordPolicy).
type ResetPasswordInput struct {
	ChallengeID ID
	OTP         OTPCode
	NewPassword Password
}
