package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

const (
	csrfVersion    byte = 1
	csrfLifetime        = time.Hour
	csrfSaltBytes       = 16
	csrfNonceBytes      = 32
	csrfTokenBytes      = 1 + 8 + csrfSaltBytes + sha256.Size
)

// SessionCSRFBinding is the binding of a token issued to a signed-in session.
func SessionCSRFBinding(sessionID domain.ID) string { return "session:" + sessionID.String() }

// PreLoginCSRFBinding is the binding of a token issued before login, tied to the random nonce in
// the browser's csrf_nonce cookie.
func PreLoginCSRFBinding(nonce string) string { return "pre:" + nonce }

// NewCSRFNonce returns the random value for the csrf_nonce cookie.
func NewCSRFNonce() (string, error) {
	raw := make([]byte, csrfNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate csrf nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// CSRFCodec issues and checks the signed CSRF tokens of phase 1 spec section 10. A token proves
// the request came from a page that asked this server for it for this session (or, before login,
// this browser): it is bound to a session ID or pre-login nonce, signed with CSRF_SECRET and
// expires after one hour. It contains no session token or hash.
type CSRFCodec struct {
	secret []byte
	clock  port.Clock
}

// NewCSRFCodec returns a codec keyed with secret (at least 32 bytes).
func NewCSRFCodec(secret []byte, clock port.Clock) (*CSRFCodec, error) {
	if clock == nil {
		return nil, errors.New("CSRF codec needs a clock")
	}
	s, err := copySecret("CSRF secret", secret)
	if err != nil {
		return nil, err
	}
	return &CSRFCodec{secret: s, clock: clock}, nil
}

// Issue returns a token bound to binding and the moment it expires (whole seconds).
// Layout: base64url(version ‖ expiry (8 bytes, big-endian unix seconds) ‖ salt (16) ‖ mac (32)).
func (c *CSRFCodec) Issue(binding string) (token string, expiresAt time.Time, err error) {
	expiresAt = time.Unix(c.clock.Now().Add(csrfLifetime).Unix(), 0).UTC()
	salt := make([]byte, csrfSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", time.Time{}, fmt.Errorf("generate csrf salt: %w", err)
	}
	raw := make([]byte, 0, csrfTokenBytes)
	raw = append(raw, csrfVersion)
	raw = binary.BigEndian.AppendUint64(raw, uint64(expiresAt.Unix())) //nolint:gosec // a unix time after 1970 is never negative.
	expiry := raw[1:9]
	raw = append(raw, salt...)
	raw = append(raw, c.mac(binding, expiry, salt)...)
	return base64.RawURLEncoding.EncodeToString(raw), expiresAt, nil
}

// Verify checks token against binding. Every failure returns the same csrf_failed error, so the
// response does not say which check failed.
func (c *CSRFCodec) Verify(token, binding string) error {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != csrfTokenBytes || raw[0] != csrfVersion {
		return csrfFailed()
	}
	expiry, salt, got := raw[1:9], raw[9:9+csrfSaltBytes], raw[9+csrfSaltBytes:]
	if !hmac.Equal(got, c.mac(binding, expiry, salt)) {
		return csrfFailed()
	}
	expiresAt := time.Unix(int64(binary.BigEndian.Uint64(expiry)), 0) //nolint:gosec // the MAC proves we wrote this value.
	if !c.clock.Now().Before(expiresAt) {
		return csrfFailed()
	}
	return nil
}

func (c *CSRFCodec) mac(binding string, expiry, salt []byte) []byte {
	m := hmac.New(sha256.New, c.secret)
	m.Write([]byte("csrf\x00" + binding + "\x00"))
	m.Write(expiry)
	m.Write(salt)
	return m.Sum(nil)
}

func csrfFailed() error { return domain.ErrCSRFFailed }
