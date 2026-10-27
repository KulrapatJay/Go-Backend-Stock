package domain

import (
	"fmt"
	"io"
	"log/slog"
)

// secret keeps a sensitive string behind a pointer. fmt does not call methods on unexported
// struct fields, so a value held in an unexported field of another struct would otherwise be
// printed through reflection; behind a pointer only an address is printed.
type secret struct{ p *string }

func newSecret(s string) secret { return secret{p: &s} }

func (s secret) reveal() string {
	if s.p == nil {
		return ""
	}
	return *s.p
}

// writeRedacted implements fmt.Formatter for the redacting types, so no verb (%d, %x, %q…) can
// print the value.
func writeRedacted(f fmt.State) { _, _ = io.WriteString(f, redacted) }

// SecretText is a sensitive value with no rules of its own, such as a password typed at login or
// a raw session token. It never prints, formats, logs or marshals its value; Reveal returns it.
type SecretText struct{ v secret }

// NewSecretText wraps s.
func NewSecretText(s string) SecretText { return SecretText{v: newSecret(s)} }

func (SecretText) String() string             { return redacted }
func (SecretText) GoString() string           { return redacted }
func (SecretText) Format(f fmt.State, _ rune) { writeRedacted(f) }
func (SecretText) LogValue() slog.Value       { return slog.StringValue(redacted) }

// MarshalJSON keeps the value out of JSON.
func (SecretText) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// Reveal returns the value. Call it only where the value itself is needed (hashing, a cookie).
func (t SecretText) Reveal() string { return t.v.reveal() }

// IsZero reports whether t holds no value.
func (t SecretText) IsZero() bool { return t.v.reveal() == "" }
