package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

const (
	sessionTokenBytes = 32
	sessionTokenChars = 43 // 32 bytes in unpadded base64url
)

// NewSessionToken returns a random token for the session cookie and the hash to store. Only the
// hash reaches the database, so a leaked row cannot be turned into a login. The hash is SHA-256 of
// the 32 raw bytes, not of the text, so a token has exactly one spelling.
func NewSessionToken() (token string, hash []byte, err error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate session token: %w", err)
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), sum[:], nil
}

// HashSessionToken returns the stored hash of a token from a cookie. ok is false for anything
// NewSessionToken could not have produced, including a second spelling of a real token (the
// unused bits of the last character set), so a bad cookie means "no session", never an error.
func HashSessionToken(token string) (hash []byte, ok bool) {
	if len(token) != sessionTokenChars {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != sessionTokenBytes {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}

// resetTokenLifetime is how long the reset token of a verified code lasts (ADR-0022): long enough
// to choose a password, as short as the code itself.
const resetTokenLifetime = 10 * time.Minute

// NewResetToken returns a random reset token and the hash to store, built like a session token: 32
// random bytes, unpadded base64url, and SHA-256 of the raw bytes so a token has one spelling.
func NewResetToken() (token string, hash []byte, err error) {
	token, hash, err = NewSessionToken()
	if err != nil {
		return "", nil, fmt.Errorf("generate reset token: %w", err)
	}
	return token, hash, nil
}

// HashResetToken returns the stored hash of a reset token; ok is false for anything NewResetToken
// could not have produced.
func HashResetToken(token string) (hash []byte, ok bool) { return HashSessionToken(token) }

// HashResetBinding is what a reset grant stores of the CSRF binding of the browser that verified
// the code. The label keeps it apart from any other hash of the same binding.
func HashResetBinding(binding string) []byte {
	sum := sha256.Sum256([]byte("password_reset\x00" + binding))
	return sum[:]
}
