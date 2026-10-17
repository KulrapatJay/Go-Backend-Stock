package domain

import (
	"strings"
	"time"
)

// HashSize is the length of every stored token, OTP, state, nonce and key hash (SHA-256/HMAC-SHA-256).
const HashSize = 32

// invariant reports a rule a server-built value broke: a bug, not bad input.
func invariant(field, rule string) *Error {
	return NewError(CodeInternal, field+" "+rule)
}

// parseEnum returns s as T when it is exactly one of allowed (case-sensitive, matching the DB CHECK).
func parseEnum[T ~string](field, s string, allowed ...T) (T, error) {
	for _, a := range allowed {
		if string(a) == s {
			return a, nil
		}
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	var zero T
	return zero, invalidInput(field, "must be one of "+strings.Join(names, ", "))
}

// isOneOf reports whether v is one of allowed; constructors use it to check server-built enums.
func isOneOf[T comparable](v T, allowed ...T) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func requireID(field string, id ID) error {
	if id.IsZero() {
		return invariant(field, "is required")
	}
	return nil
}

func requireHash(field string, h []byte) error {
	if len(h) != HashSize {
		return invariant(field, "must be 32 bytes")
	}
	return nil
}

func requireExpiry(createdAt, expiresAt time.Time) error {
	if createdAt.IsZero() {
		return invariant("created_at", "is required")
	}
	if !expiresAt.After(createdAt) {
		return invariant("expires_at", "must be after created_at")
	}
	return nil
}

// firstError returns the first non-nil error, so constructors read as a list of rules.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
