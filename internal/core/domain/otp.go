package domain

import "time"

// MaxOTPFailedAttemptsCeiling is the DB CHECK ceiling on otp_challenges.failed_attempts; the
// allowed number of attempts comes from config and must stay within 1..this value.
const MaxOTPFailedAttemptsCeiling = 20

// OTPPurpose separates the two OTP flows (otp_challenges.purpose CHECK). It is part of the OTP
// HMAC so a code issued for one flow never verifies in the other.
type OTPPurpose string

const (
	OTPPurposeEmailVerification OTPPurpose = "email_verification"
	OTPPurposePasswordReset     OTPPurpose = "password_reset"
)

// ParseOTPPurpose accepts exactly "email_verification" or "password_reset".
func ParseOTPPurpose(s string) (OTPPurpose, error) {
	return parseEnum("purpose", s, OTPPurposeEmailVerification, OTPPurposePasswordReset)
}

// OTPChallenge is one issued one-time code. Only its HMAC is stored.
type OTPChallenge struct {
	ID             ID
	UserID         ID
	Purpose        OTPPurpose
	OTPHash        []byte
	CreatedAt      time.Time
	ExpiresAt      time.Time
	FailedAttempts int
	ConsumedAt     *time.Time
	InvalidatedAt  *time.Time
}

// NewOTPChallenge checks a challenge the service is about to store: fresh and open.
func NewOTPChallenge(c OTPChallenge) (OTPChallenge, error) {
	err := firstError(
		requireID("otp_challenge.id", c.ID),
		requireID("otp_challenge.user_id", c.UserID),
		requireHash("otp_challenge.otp_hash", c.OTPHash),
		requireExpiry(c.CreatedAt, c.ExpiresAt),
	)
	switch {
	case err != nil:
	case !isOneOf(c.Purpose, OTPPurposeEmailVerification, OTPPurposePasswordReset):
		err = invariant("otp_challenge.purpose", "must be email_verification or password_reset")
	case c.FailedAttempts != 0 || c.ConsumedAt != nil || c.InvalidatedAt != nil:
		err = invariant("otp_challenge", "must be open with no failed attempts when created")
	}
	if err != nil {
		return OTPChallenge{}, err
	}
	return c, nil
}
