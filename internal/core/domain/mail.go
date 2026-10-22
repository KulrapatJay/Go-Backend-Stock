package domain

import "time"

// OTPMail is what the Mailer needs to send a one-time code. It never prints the code or the
// address: String and GoString name only the challenge, so a stray log line cannot leak either.
type OTPMail struct {
	To          Email
	OTP         OTPCode
	ChallengeID ID
	ExpiresAt   time.Time
}

func (m OTPMail) String() string   { return "OTPMail{challenge_id=" + m.ChallengeID.String() + "}" }
func (m OTPMail) GoString() string { return m.String() }
