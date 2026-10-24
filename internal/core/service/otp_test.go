package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"go-backend-stock/internal/core/domain"
)

func mustID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	return id
}

func mustOTP(t *testing.T, s string) domain.OTPCode {
	t.Helper()
	c, err := domain.ParseOTP(s)
	if err != nil {
		t.Fatalf("ParseOTP(%q) error = %v", s, err)
	}
	return c
}

func TestGenerateOTP(t *testing.T) {
	const samples = 3000
	seen := make(map[string]struct{}, samples)
	leadingZero := false
	for range samples {
		code, err := GenerateOTP()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := domain.ParseOTP(code.Reveal()); err != nil {
			t.Fatalf("GenerateOTP() = %q, which is not six digits", code.Reveal())
		}
		leadingZero = leadingZero || strings.HasPrefix(code.Reveal(), "0")
		seen[code.Reveal()] = struct{}{}
	}
	if !leadingZero {
		t.Error("no code started with 0 in 3000 draws: the format must keep leading zeros")
	}
	if len(seen) < 2900 {
		t.Errorf("only %d distinct codes in %d draws, the draw is not spread over 000000-999999", len(seen), samples)
	}
}

func TestGenerateOTPReportsRandomnessFailure(t *testing.T) {
	_, err := generateOTP(iotest.ErrReader(errors.New("entropy exhausted")))
	if err == nil {
		t.Fatal("generateOTP() ignored a failing random source")
	}
}

func TestOTPDigestFormat(t *testing.T) {
	secret := bytes.Repeat([]byte("s"), 32)
	codec, err := NewOTPCodec(secret)
	if err != nil {
		t.Fatal(err)
	}
	challengeID, userID := mustID(t), mustID(t)

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("otp\x00email_verification\x00" + challengeID.String() + "\x00" + userID.String() + "\x00042731"))
	want := mac.Sum(nil)

	got := codec.Digest(domain.OTPPurposeEmailVerification, challengeID, userID, mustOTP(t, "042731"))
	if !bytes.Equal(got, want) || len(got) != domain.HashSize {
		t.Errorf("Digest() = %x, want %x", got, want)
	}
}

func TestOTPDigestIsBoundToEveryInput(t *testing.T) {
	codec, err := NewOTPCodec(bytes.Repeat([]byte("s"), 32))
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewOTPCodec(bytes.Repeat([]byte("t"), 32))
	if err != nil {
		t.Fatal(err)
	}
	challengeID, userID := mustID(t), mustID(t)
	otp := mustOTP(t, "123456")
	base := codec.Digest(domain.OTPPurposeEmailVerification, challengeID, userID, otp)

	variants := map[string][]byte{
		"purpose":   codec.Digest(domain.OTPPurposePasswordReset, challengeID, userID, otp),
		"challenge": codec.Digest(domain.OTPPurposeEmailVerification, mustID(t), userID, otp),
		"user":      codec.Digest(domain.OTPPurposeEmailVerification, challengeID, mustID(t), otp),
		"code":      codec.Digest(domain.OTPPurposeEmailVerification, challengeID, userID, mustOTP(t, "123457")),
		"secret":    other.Digest(domain.OTPPurposeEmailVerification, challengeID, userID, otp),
	}
	for name, d := range variants {
		if bytes.Equal(d, base) {
			t.Errorf("changing the %s did not change the digest", name)
		}
	}
	if !bytes.Equal(base, codec.Digest(domain.OTPPurposeEmailVerification, challengeID, userID, otp)) {
		t.Error("Digest() is not deterministic")
	}
}

func TestOTPVerify(t *testing.T) {
	codec, err := NewOTPCodec(bytes.Repeat([]byte("s"), 32))
	if err != nil {
		t.Fatal(err)
	}
	challengeID, userID := mustID(t), mustID(t)
	stored := codec.Digest(domain.OTPPurposePasswordReset, challengeID, userID, mustOTP(t, "000123"))

	if !codec.Verify(stored, domain.OTPPurposePasswordReset, challengeID, userID, mustOTP(t, "000123")) {
		t.Error("Verify(right code) = false")
	}
	if codec.Verify(stored, domain.OTPPurposePasswordReset, challengeID, userID, mustOTP(t, "000124")) {
		t.Error("Verify(wrong code) = true")
	}
	if codec.Verify(stored, domain.OTPPurposeEmailVerification, challengeID, userID, mustOTP(t, "000123")) {
		t.Error("a code issued for password reset verified as an email verification code")
	}
	for _, bad := range [][]byte{nil, {}, stored[:31], append(bytes.Clone(stored), 0)} {
		if codec.Verify(bad, domain.OTPPurposePasswordReset, challengeID, userID, mustOTP(t, "000123")) {
			t.Errorf("Verify() accepted a stored digest of %d bytes", len(bad))
		}
	}
}

func TestNewOTPCodecRejectsShortSecretsWithoutEchoingThem(t *testing.T) {
	_, err := NewOTPCodec([]byte("short-secret-ZQXJ"))
	if err == nil {
		t.Fatal("NewOTPCodec() accepted a secret under 32 bytes")
	}
	if strings.Contains(err.Error(), "ZQXJ") {
		t.Errorf("error %q echoes the secret", err)
	}
}
