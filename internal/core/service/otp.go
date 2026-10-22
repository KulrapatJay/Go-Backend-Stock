package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"math/big"
	"strings"

	"go-backend-stock/internal/core/domain"
)

var otpRange = big.NewInt(1_000_000)

// GenerateOTP draws a uniformly distributed six-digit code from crypto/rand, leading zeros included.
func GenerateOTP() (domain.OTPCode, error) { return generateOTP(rand.Reader) }

func generateOTP(r io.Reader) (domain.OTPCode, error) {
	n, err := rand.Int(r, otpRange)
	if err != nil {
		return domain.OTPCode{}, fmt.Errorf("generate otp: %w", err)
	}
	return domain.OTPCodeFromNumber(n.Int64())
}

// OTPCodec computes and checks the HMAC digests stored in otp_challenges.otp_hash. Six digits
// have only a million values, so a plain hash would be reversible from a leaked row; the digest
// needs OTP_SECRET, which never reaches the database, and is bound to the flow, the challenge and
// the user so a code cannot be moved to another.
type OTPCodec struct{ secret []byte }

// NewOTPCodec returns a codec keyed with secret (at least 32 bytes).
func NewOTPCodec(secret []byte) (*OTPCodec, error) {
	s, err := copySecret("OTP secret", secret)
	if err != nil {
		return nil, err
	}
	return &OTPCodec{secret: s}, nil
}

// Digest returns HMAC-SHA-256(secret, "otp" ‖ 0x00 ‖ purpose ‖ 0x00 ‖ challengeID ‖ 0x00 ‖ userID ‖ 0x00 ‖ otp).
func (c *OTPCodec) Digest(purpose domain.OTPPurpose, challengeID, userID domain.ID, otp domain.OTPCode) []byte {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(strings.Join([]string{"otp", string(purpose), challengeID.String(), userID.String(), otp.Reveal()}, "\x00")))
	return mac.Sum(nil)
}

// Verify compares stored with the digest of otp in constant time.
func (c *OTPCodec) Verify(stored []byte, purpose domain.OTPPurpose, challengeID, userID domain.ID, otp domain.OTPCode) bool {
	return hmac.Equal(stored, c.Digest(purpose, challengeID, userID, otp))
}
