package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// Mailer sends the account emails. Implementations must not log the OTP or the full address.
// Sending happens outside any database transaction, and a failure never rolls back the change
// that caused the email (phase 1 spec section 10): callers log it and carry on.
type Mailer interface {
	// SendEmailVerification sends the verification code in mail to mail.To.
	SendEmailVerification(ctx context.Context, mail domain.OTPMail) error
	// SendRegistrationAttempt tells the owner of to that someone tried to register with an
	// address that already has an account. It carries no code.
	SendRegistrationAttempt(ctx context.Context, to domain.Email) error
	// SendPasswordReset sends the reset code in mail to mail.To.
	SendPasswordReset(ctx context.Context, mail domain.OTPMail) error
	// SendPasswordChanged tells the owner of to that the password was changed. It carries no code.
	SendPasswordChanged(ctx context.Context, to domain.Email) error
}
