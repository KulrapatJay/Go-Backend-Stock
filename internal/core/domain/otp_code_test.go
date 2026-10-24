package domain_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestParseOTP(t *testing.T) {
	for _, in := range []string{"000000", "012345", "999999"} {
		c, err := domain.ParseOTP(in)
		if err != nil || c.Reveal() != in {
			t.Errorf("ParseOTP(%q) = %q, %v; want it accepted", in, c.Reveal(), err)
		}
	}
	for _, in := range []string{"", "12345", "1234567", "12345a", " 12345", "12345 ", "12 345", "-12345", "+12345", "1.2345"} {
		_, err := domain.ParseOTP(in)
		requireInvalidInput(t, err, "otp")
	}
}

// Thai and Arabic-Indic digits are Unicode digits, so a check built on unicode.IsDigit would
// accept them and the HMAC comparison would then fail with no useful message.
func TestParseOTPRejectsNonASCIIDigits(t *testing.T) {
	for _, in := range []string{"๑๒๓๔๕๖", "١٢٣٤٥٦", "１２３４５６", "12345٦"} {
		_, err := domain.ParseOTP(in)
		requireInvalidInput(t, err, "otp")
	}
}

func TestOTPCodeFromNumber(t *testing.T) {
	for n, want := range map[int64]string{0: "000000", 7: "000007", 42: "000042", 123456: "123456", 999999: "999999"} {
		c, err := domain.OTPCodeFromNumber(n)
		if err != nil || c.Reveal() != want {
			t.Errorf("OTPCodeFromNumber(%d) = %q, %v; want %q", n, c.Reveal(), err, want)
		}
	}
	for _, n := range []int64{-1, 1_000_000} {
		_, err := domain.OTPCodeFromNumber(n)
		requireCode(t, err, domain.CodeInternal)
	}
}

func TestOTPCodeAndMailNeverPrintTheCode(t *testing.T) {
	const code = "042731"
	otp, err := domain.ParseOTP(code)
	if err != nil {
		t.Fatal(err)
	}
	to, err := domain.ParseEmail("user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	mail := domain.OTPMail{To: to, OTP: otp, ChallengeID: mustID(t), ExpiresAt: time.Date(2026, 10, 22, 9, 10, 0, 0, time.UTC)}

	var outputs []string
	for _, v := range []any{otp, mail} {
		outputs = append(outputs, fmt.Sprint(v), fmt.Sprintf("%v", v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v))
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(data))
	}
	for _, out := range outputs {
		if strings.Contains(out, code) {
			t.Errorf("output %q leaks the OTP", out)
		}
		if strings.Contains(out, "user@example.com") {
			t.Errorf("output %q leaks the address", out)
		}
	}
	if !strings.Contains(fmt.Sprint(mail), mail.ChallengeID.String()) {
		t.Error("OTPMail.String() should name the challenge so log lines can be correlated")
	}
}
