package domain_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

// holder keeps the values in unexported fields: fmt prints those through reflection and never
// calls their methods, which is how a value would leak from a request struct in a log line.
// PasswordHash is left out: it is a plain string type, so an unexported field of that type still
// prints its value because fmt cannot be stopped from reading it.
type holder struct {
	pw   domain.Password
	otp  domain.OTPCode
	text domain.SecretText
}

func TestSecretsNeverPrintWithAnyVerb(t *testing.T) {
	pw, err := domain.ParsePassword("Zq9!kVx1", domain.Email{})
	if err != nil {
		t.Fatal(err)
	}
	otp, err := domain.ParseOTP("042731")
	if err != nil {
		t.Fatal(err)
	}
	text := domain.NewSecretText("raw-token-ZQXJ")
	hash := domain.PasswordHash("$argon2id$secret-hash-ZQXJ")
	h := holder{pw: pw, otp: otp, text: text}
	secrets := []string{"Zq9!kVx1", "042731", "raw-token-ZQXJ", "secret-hash-ZQXJ"}

	values := []any{pw, &pw, otp, &otp, text, &text, hash, &hash, h, &h}
	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x", "%X", "%10v"}
	var outputs []string
	for _, v := range values {
		for _, verb := range verbs {
			outputs = append(outputs, fmt.Sprintf(verb, v))
		}
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(data))
	}
	var logs bytes.Buffer
	for _, handler := range []slog.Handler{slog.NewJSONHandler(&logs, nil), slog.NewTextHandler(&logs, nil)} {
		slog.New(handler).Info("values", "pw", pw, "otp", otp, "text", text, "hash", hash, "holder", h)
	}
	outputs = append(outputs, logs.String())

	for _, out := range outputs {
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Errorf("output %q leaks %q", out, s)
			}
		}
	}
}

func TestSecretsStillReveal(t *testing.T) {
	text := domain.NewSecretText("raw-token")
	if text.Reveal() != "raw-token" || text.IsZero() || !(domain.SecretText{}).IsZero() || (domain.SecretText{}).Reveal() != "" {
		t.Error("SecretText Reveal/IsZero misbehave")
	}
	pw, err := domain.ParsePassword("Zq9!kVx1", domain.Email{})
	if err != nil || pw.Reveal() != "Zq9!kVx1" || pw.IsZero() || !(domain.Password{}).IsZero() {
		t.Error("Password Reveal/IsZero misbehave")
	}
	otp, err := domain.ParseOTP("000123")
	if err != nil || otp.Reveal() != "000123" {
		t.Error("OTPCode Reveal misbehaves")
	}
	if (domain.OTPCode{}).Reveal() != "" {
		t.Error("zero OTPCode must reveal an empty string")
	}
}
