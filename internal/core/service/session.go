package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
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
