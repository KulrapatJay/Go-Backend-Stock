package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

const (
	// oidcFlowLifetime is how long a Google flow may wait for its callback (design section 6).
	oidcFlowLifetime = 10 * time.Minute
	oidcValueBytes   = 32
)

// OIDCCodec derives the PKCE verifier of a Google flow (phase 1 spec section 10). The verifier is
// never stored: the callback recomputes it from the state in the query and the binding cookie, so
// rotating OIDC_SECRET only breaks the flows started in the last ten minutes. Safe for concurrent use.
type OIDCCodec struct{ secret []byte }

// NewOIDCCodec returns a codec keyed with secret (at least 32 bytes).
func NewOIDCCodec(secret []byte) (*OIDCCodec, error) {
	s, err := copySecret("OIDC secret", secret)
	if err != nil {
		return nil, err
	}
	return &OIDCCodec{secret: s}, nil
}

// Verifier returns base64url(HMAC-SHA-256(secret, "pkce" ‖ 0x00 ‖ state ‖ 0x00 ‖ binding)): 43
// characters, inside the 43–128 that RFC 7636 allows.
func (c *OIDCCodec) Verifier(state, binding string) string {
	mac := hmac.New(sha256.New, c.secret)
	for i, part := range []string{"pkce", state, binding} {
		if i > 0 {
			mac.Write([]byte{0})
		}
		mac.Write([]byte(part))
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// CodeChallenge is the S256 challenge of verifier (RFC 7636 section 4.2).
func CodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewOIDCValue returns 32 random bytes as 43 unpadded base64url characters: a state, a nonce or a
// browser binding.
func NewOIDCValue() (string, error) {
	raw := make([]byte, oidcValueBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate OIDC value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// ValidOIDCValue reports whether s has the shape NewOIDCValue produces. A callback value of any
// other shape cannot belong to a flow, so it never reaches the database.
func ValidOIDCValue(s string) bool {
	if len(s) != 43 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(raw) == oidcValueBytes
}

// HashOIDCValue is what oauth_flows stores instead of a state, nonce or binding.
func HashOIDCValue(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
