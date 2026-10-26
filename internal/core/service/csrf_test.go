package service

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func newTestCSRF(t *testing.T) (*CSRFCodec, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	codec, err := NewCSRFCodec([]byte(strings.Repeat("s", 32)), clock)
	if err != nil {
		t.Fatal(err)
	}
	return codec, clock
}

func TestCSRFIssueAndVerify(t *testing.T) {
	codec, clock := newTestCSRF(t)
	binding := SessionCSRFBinding(mustID(t))

	token, expiresAt, err := codec.Issue(binding)
	if err != nil {
		t.Fatal(err)
	}
	if want := clock.Now().Add(time.Hour); !expiresAt.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", expiresAt, want)
	}
	if err := codec.Verify(token, binding); err != nil {
		t.Errorf("Verify() error = %v", err)
	}

	clock.Advance(time.Hour - time.Second)
	if err := codec.Verify(token, binding); err != nil {
		t.Errorf("Verify() one second before expiry error = %v", err)
	}
	clock.Advance(time.Second)
	_ = requireCode(t, codec.Verify(token, binding), domain.CodeCSRFFailed) // expired at the expiry second
}

func TestCSRFTokenLayout(t *testing.T) {
	codec, clock := newTestCSRF(t)
	token, expiresAt, err := codec.Issue("pre:abc")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 57 || raw[0] != 1 {
		t.Fatalf("token is %d bytes, want 57 bytes starting with version byte 1", len(raw))
	}
	var expiry uint64
	for _, b := range raw[1:9] {
		expiry = expiry<<8 | uint64(b)
	}
	if int64(expiry) != expiresAt.Unix() || expiresAt.Unix() != clock.Now().Add(time.Hour).Unix() {
		t.Errorf("embedded expiry = %d, want %d (big-endian unix seconds)", expiry, expiresAt.Unix())
	}

	again, _, err := codec.Issue("pre:abc")
	if err != nil || again == token {
		t.Errorf("two tokens for one binding are equal (%v): the salt must differ", err)
	}
}

func TestCSRFVerifyRejectsEverythingElseWithOneMessage(t *testing.T) {
	codec, clock := newTestCSRF(t)
	otherCodec, err := NewCSRFCodec([]byte(strings.Repeat("t", 32)), clock)
	if err != nil {
		t.Fatal(err)
	}
	sessionBinding := SessionCSRFBinding(mustID(t))
	token, _, err := codec.Issue(sessionBinding)
	if err != nil {
		t.Fatal(err)
	}
	preToken, _, err := codec.Issue(PreLoginCSRFBinding("nonce"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := otherCodec.Issue(sessionBinding)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct{ token, binding string }{
		"empty":                      {"", sessionBinding},
		"not base64":                 {"!!!", sessionBinding},
		"other session":              {token, SessionCSRFBinding(mustID(t))},
		"pre-login token on session": {preToken, sessionBinding},
		"session token on pre-login": {token, PreLoginCSRFBinding("nonce")},
		"other secret":               {foreign, sessionBinding},
		"one byte short":             {base64.RawURLEncoding.EncodeToString(raw[:56]), sessionBinding},
		"one byte long":              {base64.RawURLEncoding.EncodeToString(append(slices.Clone(raw), 0)), sessionBinding},
	}
	// Flipping any single bit anywhere in the token must break it: version, expiry, salt or MAC.
	for i := range raw {
		mutated := slices.Clone(raw)
		mutated[i] ^= 0x01
		cases[fmt.Sprintf("bit flipped in byte %d", i)] = struct{ token, binding string }{
			base64.RawURLEncoding.EncodeToString(mutated), sessionBinding,
		}
	}

	messages := map[string]bool{}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			de := requireCode(t, codec.Verify(c.token, c.binding), domain.CodeCSRFFailed)
			messages[de.Message] = true
		})
	}
	if len(messages) != 1 {
		t.Errorf("Verify() produced %d different messages, want one: %v", len(messages), messages)
	}
}

func TestCSRFNonce(t *testing.T) {
	a, err := NewCSRFNonce()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewCSRFNonce()
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil || len(raw) != 32 || a == b {
		t.Errorf("nonce %q decodes to %d bytes (%v), equal to the next one: %v", a, len(raw), err, a == b)
	}
	if got := SessionCSRFBinding(mustID(t)); !strings.HasPrefix(got, "session:") {
		t.Errorf("session binding = %q", got)
	}
	if got := PreLoginCSRFBinding(a); got != "pre:"+a {
		t.Errorf("pre-login binding = %q", got)
	}
}

func TestNewCSRFCodecRejectsShortSecret(t *testing.T) {
	if _, err := NewCSRFCodec([]byte("short"), newFakeClock()); err == nil {
		t.Error("NewCSRFCodec() accepted a secret under 32 bytes")
	}
	if _, err := NewCSRFCodec([]byte(strings.Repeat("s", 32)), nil); err == nil {
		t.Error("NewCSRFCodec() accepted a nil clock, which would panic on the first request")
	}
}
