package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestOIDCValuesNeverPrintTheirSecrets(t *testing.T) {
	values := map[string]any{
		"callback": domain.OIDCCallback{Code: domain.NewSecretText("code-ZQXJ"), State: "s", BrowserBinding: domain.NewSecretText("binding-ZQXJ")},
		"exchange": domain.CodeExchange{Code: domain.NewSecretText("code-ZQXJ"), CodeVerifier: domain.NewSecretText("verifier-ZQXJ")},
		"redirect": domain.OIDCRedirect{URL: "https://accounts.example/auth", BrowserBinding: domain.NewSecretText("binding-ZQXJ"), ExpiresAt: time.Now()},
		"result":   domain.OIDCResult{Outcome: domain.OIDCOutcomeSuccess, Session: domain.SessionGrant{Token: domain.NewSecretText("token-ZQXJ")}},
	}
	for name, v := range values {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			if out := fmt.Sprintf(verb, v); strings.Contains(out, "ZQXJ") {
				t.Errorf("%s %s leaks a secret: %s", name, verb, out)
			}
		}
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "ZQXJ") {
			t.Errorf("%s JSON leaks a secret: %s", name, data)
		}
	}
}

func TestOIDCOutcomesAreTheRedirectValues(t *testing.T) {
	got := []domain.OIDCOutcome{domain.OIDCOutcomeSuccess, domain.OIDCOutcomeLinked, domain.OIDCOutcomeLinkRequired, domain.OIDCOutcomeFailed}
	want := []string{"success", "linked", "link_required", "failed"}
	for i := range got {
		if string(got[i]) != want[i] {
			t.Errorf("outcome %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGoogleSentinels(t *testing.T) {
	for sentinel, code := range map[*domain.Error]domain.ErrorCode{
		domain.ErrFeatureDisabled:  domain.CodeFeatureDisabled,
		domain.ErrIdentityRejected: domain.CodeIdentityRejected,
	} {
		if sentinel.Code != code || sentinel.Message == "" {
			t.Errorf("sentinel %v has code %s, want %s", sentinel, sentinel.Code, code)
		}
		if !errors.Is(fmt.Errorf("wrapped: %w", sentinel), sentinel) {
			t.Errorf("a wrapped %s must still match", code)
		}
	}
	if string(domain.CodeIdentityRejected) != "identity_rejected" {
		t.Errorf("CodeIdentityRejected = %q, want identity_rejected", domain.CodeIdentityRejected)
	}
}

func TestIdentityRejectedDoesNotMatchPasswordFailure(t *testing.T) {
	if errors.Is(domain.ErrIdentityRejected, domain.ErrInvalidCredentials) {
		t.Error("a refused Google sign-in must not match ErrInvalidCredentials")
	}
	if errors.Is(domain.ErrInvalidCredentials, domain.ErrIdentityRejected) {
		t.Error("a failed password login must not match ErrIdentityRejected")
	}
}
