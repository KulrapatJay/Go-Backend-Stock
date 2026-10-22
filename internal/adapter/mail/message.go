// Package mail implements port.Mailer: SMTPMailer sends over SMTP and Dispatcher hands emails to
// a background worker so a response never waits for the mail server.
package mail

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
	"unicode"

	"go-backend-stock/internal/core/domain"
)

const (
	verificationSubject        = "Your verification code"
	registrationAttemptSubject = "Someone tried to register with your email address"
	otpExpiryLayout            = "2006-01-02 15:04"
)

func unsafeMail(rule string) error {
	return domain.NewError(domain.CodeInternal, "mail: "+rule)
}

// checkAddress rejects anything that could change the structure of an SMTP command or a header:
// line breaks (a second header or command), separators and brackets (a second recipient),
// quotes, whitespace and control characters. domain.ParseEmail already refuses these, so a hit
// here means a bug or a value that did not come from the domain.
func checkAddress(addr string) error {
	local, host, ok := strings.Cut(addr, "@")
	switch {
	case !ok || local == "" || host == "" || strings.Contains(host, "@"):
		return unsafeMail("address must have the form name@host")
	case strings.ContainsAny(addr, "\r\n,;<>\""),
		strings.ContainsFunc(addr, unicode.IsSpace),
		strings.ContainsFunc(addr, unicode.IsControl):
		return unsafeMail("address contains a character that is not allowed")
	}
	return nil
}

// checkHeaderValue accepts only printable ASCII without line breaks. Subjects are fixed ASCII
// strings; anything else would need RFC 2047 encoding, which this package does not do yet.
func checkHeaderValue(v string) error {
	if strings.ContainsFunc(v, func(r rune) bool { return r > unicode.MaxASCII || unicode.IsControl(r) }) {
		return unsafeMail("header value must be printable ASCII on one line")
	}
	return nil
}

// message is one email, built as plain text UTF-8 by hand so no header comes from a template.
type message struct {
	from, to, subject, body string
	date                    time.Time
	id                      string // Message-ID without the angle brackets
}

func (m message) bytes() ([]byte, error) {
	for _, addr := range []string{m.from, m.to} {
		if err := checkAddress(addr); err != nil {
			return nil, err
		}
	}
	for _, v := range []string{m.subject, m.id} {
		if err := checkHeaderValue(v); err != nil {
			return nil, err
		}
	}
	var b strings.Builder
	header := func(name, value string) { b.WriteString(name + ": " + value + "\r\n") }
	header("From", "<"+m.from+">")
	header("To", "<"+m.to+">")
	header("Subject", m.subject)
	header("Date", m.date.UTC().Format(time.RFC1123Z))
	header("Message-ID", "<"+m.id+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(m.body, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(b.String()), nil
}

// newMessageID returns 16 random bytes in hex followed by "@" and the host of the sender address.
func newMessageID(fromAddr string) (string, error) {
	_, host, _ := strings.Cut(fromAddr, "@")
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw) + "@" + host, nil
}

func verificationBody(m domain.OTPMail) string {
	return "Your verification code is " + m.OTP.Reveal() + ".\n\n" +
		"It expires at " + m.ExpiresAt.UTC().Format(otpExpiryLayout) + " UTC. Do not share it with anyone.\n\n" +
		"Reference: " + m.ChallengeID.String() + "\n\n" +
		"If you did not create an account, you can ignore this email.\n"
}

func registrationAttemptBody() string {
	return "Someone tried to create an account with this email address, but an account already exists.\n\n" +
		"If it was you, sign in instead, or use the password reset option if you forgot your password.\n" +
		"If it was not you, you can ignore this email.\n"
}
