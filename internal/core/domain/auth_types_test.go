package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestNewRequestMeta(t *testing.T) {
	id := mustID(t)
	m := domain.NewRequestMeta(id, netip.MustParseAddr("::ffff:203.0.113.77"), "agent\r\nX-Injected: yes")
	if m.RequestID != id || m.ClientIP != netip.MustParseAddr("203.0.113.77") || m.MaskedIP != "203.0.113.0/24" {
		t.Errorf("meta = %+v, want the unmapped IP, its /24 and the request ID", m)
	}
	if strings.ContainsAny(m.UserAgent, "\r\n") {
		t.Errorf("user agent %q was not sanitized", m.UserAgent)
	}

	unknown := domain.NewRequestMeta(id, netip.Addr{}, "")
	if unknown.ClientIP.IsValid() || unknown.MaskedIP != "" {
		t.Errorf("meta without an IP = %+v, want no IP and no mask", unknown)
	}
}

func TestPrincipalIsZero(t *testing.T) {
	if !(domain.Principal{}).IsZero() || (domain.Principal{UserID: mustID(t), SessionID: mustID(t)}).IsZero() {
		t.Error("Principal.IsZero misbehaves")
	}
}

func TestSessionGrantNeverPrintsTheToken(t *testing.T) {
	g := domain.SessionGrant{Token: domain.NewSecretText("raw-session-token-ZQXJ"), ExpiresAt: time.Now()}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if out := fmt.Sprintf(verb, g); strings.Contains(out, "ZQXJ") {
			t.Errorf("%s leaks the token: %s", verb, out)
		}
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ZQXJ") {
		t.Errorf("JSON leaks the token: %s", data)
	}
}

func TestRetryErrorAndAuthSentinels(t *testing.T) {
	e := domain.NewRetryError(domain.CodeRateLimited, "too many requests", 1500*time.Millisecond)
	if e.RetryAfter != 1500*time.Millisecond || !errors.Is(e, domain.NewError(domain.CodeRateLimited, "other text")) {
		t.Errorf("NewRetryError() = %+v", e)
	}
	if domain.NewError(domain.CodeInternal, "x").RetryAfter != 0 {
		t.Error("NewError must not set RetryAfter")
	}

	want := map[*domain.Error]domain.ErrorCode{
		domain.ErrUnauthenticated:     domain.CodeUnauthenticated,
		domain.ErrInvalidCredentials:  domain.CodeInvalidCredentials,
		domain.ErrInvalidVerification: domain.CodeInvalidVerification,
		domain.ErrEmailNotVerified:    domain.CodeEmailNotVerified,
		domain.ErrCSRFFailed:          domain.CodeCSRFFailed,
	}
	for sentinel, code := range want {
		if sentinel.Code != code || sentinel.Message == "" {
			t.Errorf("sentinel %v has code %s, want %s", sentinel, sentinel.Code, code)
		}
		if !errors.Is(fmt.Errorf("wrapped: %w", sentinel), domain.NewError(code, "any")) {
			t.Errorf("a wrapped %s must still match its code", code)
		}
	}
}
