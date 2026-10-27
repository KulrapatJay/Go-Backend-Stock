package domain

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

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

// OTPLength is the number of digits in an OTP.
const OTPLength = 6

// OTPCode is a six-digit one-time code. It prints, formats and marshals as [REDACTED]; Reveal
// returns the digits for hashing and for the email body only.
type OTPCode struct{ v secret }

func (OTPCode) String() string             { return redacted }
func (OTPCode) GoString() string           { return redacted }
func (OTPCode) Format(f fmt.State, _ rune) { writeRedacted(f) }
func (OTPCode) LogValue() slog.Value       { return slog.StringValue(redacted) }

// MarshalJSON keeps the code out of JSON logs and responses.
func (OTPCode) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// Reveal returns the digits. Call it only to compute the digest or to write the email.
func (c OTPCode) Reveal() string { return c.v.reveal() }

// ParseOTP accepts exactly six ASCII digits ("0".."9"). Other Unicode digits such as Thai or
// Arabic-Indic numerals are rejected: they would never match the digest of the issued code.
func ParseOTP(s string) (OTPCode, error) {
	if len(s) != OTPLength {
		return OTPCode{}, invalidInput("otp", "must be 6 digits")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return OTPCode{}, invalidInput("otp", "must be 6 digits")
		}
	}
	return OTPCode{v: newSecret(s)}, nil
}

// OTPCodeFromNumber formats a number from 0 to 999999 as six digits with leading zeros. The
// server calls it with a value it drew itself, so a number out of range is an internal error.
func OTPCodeFromNumber(n int64) (OTPCode, error) {
	if n < 0 || n > 999999 {
		return OTPCode{}, invariant("otp", "number must be between 0 and 999999")
	}
	s := strconv.FormatInt(n, 10)
	return OTPCode{v: newSecret(strings.Repeat("0", OTPLength-len(s)) + s)}, nil
}
