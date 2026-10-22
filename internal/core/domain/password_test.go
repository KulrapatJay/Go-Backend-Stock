package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func passwordTestEmail(t *testing.T, s string) domain.Email {
	t.Helper()
	e, err := domain.ParseEmail(s)
	if err != nil {
		t.Fatalf("ParseEmail(%q) error = %v", s, err)
	}
	return e
}

// reasonsOf returns the reason codes of an invalid_input password error, or nil when err is nil.
func reasonsOf(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	requireInvalidInput(t, err, "password")
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want *domain.Error", err, err)
	}
	return de.Reasons
}

func TestParsePassword(t *testing.T) {
	const (
		short     = "password_too_short"
		long      = "password_too_long"
		nonASCII  = "password_non_ascii"
		space     = "password_contains_space"
		noLower   = "password_missing_lowercase"
		noUpper   = "password_missing_uppercase"
		noSpecial = "password_missing_special"
		withEmail = "password_contains_email"
	)
	tests := []struct {
		name  string
		email string // default somebody@example.com
		in    string
		want  []string
	}{
		{name: "valid minimum length", in: "Ab!def"},
		{name: "valid maximum length", in: "Aa!" + strings.Repeat("x", 29)},
		{name: "too long", in: "Aa!" + strings.Repeat("x", 30), want: []string{long}},
		{name: "too short", in: "Ab!", want: []string{short}},
		{name: "empty", in: "", want: []string{short, noLower, noUpper, noSpecial}},
		{name: "only lowercase", in: "abcdefgh", want: []string{noUpper, noSpecial}},
		{name: "no lowercase", in: "ABCDE!12", want: []string{noLower}},
		{name: "digits are not special characters", in: "Abcdef12", want: []string{noSpecial}},
		{name: "space", in: "Abc d!ef", want: []string{space}},
		{name: "tab", in: "Abc\td!ef", want: []string{space}},
		{name: "no-break space", in: "Abc\u00a0d!ef", want: []string{nonASCII, space}},
		{name: "accented letter", in: "Pässword!1", want: []string{nonASCII}},
		{name: "thai only", in: "รหัสผ่าน", want: []string{nonASCII, noLower, noUpper, noSpecial}},
		{name: "control character", in: "Abc!12\x00x", want: []string{nonASCII}},
		{name: "invalid utf-8", in: "Abc!12\xffx", want: []string{nonASCII}},
		{name: "contains the email local part", in: "XSomeBody!1", want: []string{withEmail}},
		{name: "email local part in other case", in: "SOMEBODY-a1", want: []string{withEmail}},
		{name: "email local part shorter than four", email: "abc@example.com", in: "Abc!1234"},
		{name: "several reasons in a stable order", in: "ab", want: []string{short, noUpper, noSpecial}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email := tt.email
			if email == "" {
				email = "somebody@example.com"
			}
			p, err := domain.ParsePassword(tt.in, passwordTestEmail(t, email))
			got := reasonsOf(t, err)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("reasons = %v, want %v", got, tt.want)
			}
			if err == nil && p.Reveal() != tt.in {
				t.Errorf("Reveal() did not return the accepted password")
			}
		})
	}
}

func TestParsePasswordWithoutEmail(t *testing.T) {
	if _, err := domain.ParsePassword("Ab!def", domain.Email{}); err != nil {
		t.Fatalf("ParsePassword() with a zero email error = %v", err)
	}
}

func TestParsePasswordCheckedAsksTheCommonListWithLowercase(t *testing.T) {
	email := passwordTestEmail(t, "somebody@example.com")
	var seen []string
	common := func(lowercase string) bool {
		seen = append(seen, lowercase)
		return lowercase == "p@ssw0rd"
	}

	_, err := domain.ParsePasswordChecked("P@ssw0rd", email, common)
	if got := reasonsOf(t, err); !slices.Equal(got, []string{"password_too_common"}) {
		t.Fatalf("reasons = %v, want [password_too_common]", got)
	}
	if !slices.Equal(seen, []string{"p@ssw0rd"}) {
		t.Errorf("common list was asked %v, want [p@ssw0rd]", seen)
	}

	if _, err := domain.ParsePasswordChecked("Zq9!kVx1", email, common); err != nil {
		t.Errorf("a password that is not on the list error = %v", err)
	}
	if _, err := domain.ParsePasswordChecked("P@ssw0rd", email, nil); err != nil {
		t.Errorf("a nil common list must skip the check, error = %v", err)
	}
}

func TestPasswordNeverPrintsItself(t *testing.T) {
	const secret = "Zq9!kVx1"
	p, err := domain.ParsePassword(secret, domain.Email{})
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{
		fmt.Sprint(p), fmt.Sprintf("%v", p), fmt.Sprintf("%+v", p), fmt.Sprintf("%#v", p), fmt.Sprintf("%q", p),
	} {
		if strings.Contains(out, secret) {
			t.Errorf("formatted password %q leaks the secret", out)
		}
	}
	data, err := json.Marshal(struct{ Password domain.Password }{p})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || !strings.Contains(string(data), "[REDACTED]") {
		t.Errorf("JSON = %s, want the secret redacted", data)
	}
	if p.Reveal() != secret || p.IsZero() || !(domain.Password{}).IsZero() {
		t.Error("Reveal/IsZero misbehave")
	}
}
