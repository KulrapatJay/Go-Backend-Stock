package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

const base64URLAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

func TestNewSessionToken(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 43 || strings.ContainsAny(token, "=+/") {
		t.Errorf("token %q must be 43 base64url characters without padding", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token decodes to %d bytes (%v), want 32", len(raw), err)
	}
	if sum := sha256.Sum256(raw); !bytes.Equal(hash, sum[:]) {
		t.Error("the hash must be SHA-256 of the 32 raw bytes, not of the string")
	}

	other, _, err := NewSessionToken()
	if err != nil || other == token {
		t.Errorf("two tokens are equal (%v)", err)
	}
}

func TestHashSessionToken(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := HashSessionToken(token)
	if !ok || !bytes.Equal(got, hash) {
		t.Errorf("HashSessionToken() = %x, %v; want the hash NewSessionToken returned", got, ok)
	}

	for name, bad := range map[string]string{
		"empty":      "",
		"short":      token[:42],
		"long":       token + "A",
		"padded":     token + "=",
		"standard":   strings.NewReplacer("-", "+", "_", "/").Replace(token) + "=", // wrong alphabet and padding
		"not base64": strings.Repeat("!", 43),
		"whitespace": " " + token[1:],
	} {
		if _, ok := HashSessionToken(bad); ok {
			t.Errorf("%s: HashSessionToken(%q) accepted it", name, bad)
		}
	}
}

// 32 bytes need 43 characters, but the last one carries only 4 data bits; a lenient decoder
// ignores the other 2, so several strings would name the same session.
func TestHashSessionTokenRejectsNonCanonicalEncoding(t *testing.T) {
	token, _, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	last := strings.IndexByte(base64URLAlphabet, token[42])
	mutated := token[:42] + string(base64URLAlphabet[last|1]) // sets a padding bit
	if mutated == token {
		t.Fatal("test bug: the mutation changed nothing")
	}
	if raw, err := base64.RawURLEncoding.DecodeString(mutated); err != nil || len(raw) != 32 {
		t.Fatalf("test bug: a lenient decoder should accept %q (%v)", mutated, err)
	}
	if _, ok := HashSessionToken(mutated); ok {
		t.Errorf("HashSessionToken(%q) accepted a second spelling of the same token", mutated)
	}
}
