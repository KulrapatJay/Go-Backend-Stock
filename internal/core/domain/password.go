package domain

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	minPasswordRunes = 6
	maxPasswordRunes = 32
	// minEmailLocalPartRunes is how long the part of the email before "@" must be before a
	// password containing it is rejected; shorter parts would match by chance too often.
	minEmailLocalPartRunes = 4
)

// PasswordReason is a stable code for one rule a password broke (phase 1 spec section 10).
type PasswordReason string

const (
	PasswordReasonTooShort         PasswordReason = "password_too_short"
	PasswordReasonTooLong          PasswordReason = "password_too_long"
	PasswordReasonNonASCII         PasswordReason = "password_non_ascii"
	PasswordReasonContainsSpace    PasswordReason = "password_contains_space"
	PasswordReasonMissingLowercase PasswordReason = "password_missing_lowercase"
	PasswordReasonMissingUppercase PasswordReason = "password_missing_uppercase"
	PasswordReasonMissingSpecial   PasswordReason = "password_missing_special"
	PasswordReasonTooCommon        PasswordReason = "password_too_common"
	PasswordReasonContainsEmail    PasswordReason = "password_contains_email"
)

// Password is a password that passed the policy. It prints, formats and marshals as [REDACTED];
// Reveal returns the real value for hashing only.
type Password struct{ v secret }

func (Password) String() string             { return redacted }
func (Password) GoString() string           { return redacted }
func (Password) Format(f fmt.State, _ rune) { writeRedacted(f) }
func (Password) LogValue() slog.Value       { return slog.StringValue(redacted) }

// MarshalJSON keeps the password out of JSON logs and responses.
func (Password) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// Reveal returns the password. Call it only to hash the value.
func (p Password) Reveal() string { return p.v.reveal() }

// IsZero reports whether p is the zero value (no password).
func (p Password) IsZero() bool { return p.v.reveal() == "" }

// ParsePassword checks raw against every password rule except the common-password list, which
// needs a data file and is checked by service.PasswordPolicy through ParsePasswordChecked. The
// error is invalid_input with one reason code per broken rule, all at once and in a stable order.
// Used for register and password reset; login never calls it (phase 1 spec section 10).
func ParsePassword(raw string, email Email) (Password, error) {
	return ParsePasswordChecked(raw, email, nil)
}

// ParsePasswordChecked is ParsePassword plus the common-password check. isCommon receives the
// password converted to lower case; nil skips the check.
func ParsePasswordChecked(raw string, email Email, isCommon func(lowercase string) bool) (Password, error) {
	reasons := passwordReasons(raw, email, isCommon)
	if len(reasons) > 0 {
		return Password{}, NewReasonsError(CodeInvalidInput, "password does not meet the requirements", reasons...)
	}
	return Password{v: newSecret(raw)}, nil
}

func passwordReasons(raw string, email Email, isCommon func(lowercase string) bool) []string {
	var hasLower, hasUpper, hasSpecial, hasSpace, hasNonASCII bool
	for _, r := range raw {
		space := unicode.IsSpace(r)
		hasSpace = hasSpace || space
		switch {
		case r > 0x7e, r < 0x21 && !space: // outside printable ASCII; plain whitespace is reported as a space
			hasNonASCII = true
		case space:
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r < '0' || r > '9': // the remaining printable ASCII is punctuation
			hasSpecial = true
		}
	}

	var reasons []string
	add := func(broken bool, reason PasswordReason) {
		if broken {
			reasons = append(reasons, string(reason))
		}
	}
	n := utf8.RuneCountInString(raw)
	lower := strings.ToLower(raw)
	add(n < minPasswordRunes, PasswordReasonTooShort)
	add(n > maxPasswordRunes, PasswordReasonTooLong)
	add(hasNonASCII, PasswordReasonNonASCII)
	add(hasSpace, PasswordReasonContainsSpace)
	add(!hasLower, PasswordReasonMissingLowercase)
	add(!hasUpper, PasswordReasonMissingUppercase)
	add(!hasSpecial, PasswordReasonMissingSpecial)
	add(isCommon != nil && isCommon(lower), PasswordReasonTooCommon)
	add(containsEmailLocalPart(lower, email), PasswordReasonContainsEmail)
	return reasons
}

// containsEmailLocalPart reports whether lower contains the part of the email before "@", when
// that part is long enough to be meaningful. Email is already lower case.
func containsEmailLocalPart(lower string, email Email) bool {
	local, _, _ := strings.Cut(email.String(), "@")
	return utf8.RuneCountInString(local) >= minEmailLocalPartRunes && strings.Contains(lower, local)
}
