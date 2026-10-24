package service

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func mustEmail(t *testing.T, s string) domain.Email {
	t.Helper()
	e, err := domain.ParseEmail(s)
	if err != nil {
		t.Fatalf("ParseEmail(%q) error = %v", s, err)
	}
	return e
}

func reasonsOf(t *testing.T, err error) []string {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != domain.CodeInvalidInput {
		t.Fatalf("error = %v, want invalid_input", err)
	}
	return de.Reasons
}

func TestPasswordPolicyParse(t *testing.T) {
	list, err := NewBlocklist(strings.NewReader("p@ssw0rd\nqwerty!1a\n"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewPasswordPolicy(list)
	if err != nil {
		t.Fatal(err)
	}
	email := mustEmail(t, "somebody@example.com")

	if _, err := policy.Parse("Zq9!kVx1", email); err != nil {
		t.Errorf("a strong password error = %v", err)
	}

	_, err = policy.Parse("p@ssW0rd", email) // matched after lower-casing
	if got := reasonsOf(t, err); !slices.Equal(got, []string{"password_too_common"}) {
		t.Errorf("reasons = %v, want [password_too_common]", got)
	}

	_, err = policy.Parse("p@ssw0rd", email) // the other rules are still reported with it
	if got := reasonsOf(t, err); !slices.Equal(got, []string{"password_missing_uppercase", "password_too_common"}) {
		t.Errorf("reasons = %v, want missing_uppercase and too_common", got)
	}
}

func TestNewPasswordPolicyNeedsABlocklist(t *testing.T) {
	if _, err := NewPasswordPolicy(nil); err == nil {
		t.Error("NewPasswordPolicy(nil) must fail: a nil list would silently turn the check off")
	}
}
