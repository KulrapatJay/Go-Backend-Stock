package domain

import "time"

// SessionGrant is the result of a successful login. Token is the raw value for the session cookie;
// only the HTTP adapter reads it, to set the cookie.
type SessionGrant struct {
	User      User
	Token     SecretText
	ExpiresAt time.Time
}

// OTPReceipt is the answer to register and to "send the code again". It has the same shape whether
// or not a code was sent, so it does not reveal whether the email has an account.
type OTPReceipt struct {
	ChallengeID ID
	ExpiresIn   time.Duration
}
