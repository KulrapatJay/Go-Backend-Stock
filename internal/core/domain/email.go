package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxEmailRunes = 254

// Email is a trimmed, lower-cased address. Dots and +suffixes are kept as typed.
type Email struct{ value string }

// ParseEmail checks the shape (one "@" with text on both sides) and rejects the characters that
// could change an SMTP command or a mail header, which would let one address address several
// people, and invisible characters: ownership is proven by the verification OTP, not by syntax.
func ParseEmail(s string) (Email, error) {
	v := strings.TrimSpace(s)
	if !utf8.ValidString(v) {
		return Email{}, invalidInput("email", "must be valid UTF-8")
	}
	v = strings.ToLower(v)
	n := utf8.RuneCountInString(v)
	if n == 0 {
		return Email{}, invalidInput("email", "is required")
	}
	if n > maxEmailRunes {
		return Email{}, invalidInput("email", "must be at most 254 characters")
	}
	if strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return Email{}, invalidInput("email", "must not contain spaces or control characters")
	}
	if strings.ContainsAny(v, `"(),:;<>[\]`) || hasHidden(v) {
		return Email{}, invalidInput("email", `must not contain " ( ) , : ; < > [ \ ] or invisible characters`)
	}
	local, domainPart, ok := strings.Cut(v, "@")
	if !ok || local == "" || domainPart == "" || strings.Contains(domainPart, "@") {
		return Email{}, invalidInput("email", "must be an address such as name@example.com")
	}
	return Email{value: v}, nil
}

func (e Email) String() string { return e.value }

// IsZero reports whether e is the zero value (no email).
func (e Email) IsZero() bool { return e.value == "" }
