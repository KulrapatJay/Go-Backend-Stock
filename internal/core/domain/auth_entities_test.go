package domain_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

var (
	t0      = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	hash32  = bytes.Repeat([]byte{7}, domain.HashSize)
	hash31  = bytes.Repeat([]byte{7}, domain.HashSize-1)
	revoked = t0.Add(time.Minute)
)

func TestEnumsMatchDBChecksExactly(t *testing.T) {
	ok := map[string]func(string) error{
		"logout":             func(s string) error { _, err := domain.ParseRevokeReason(s); return err },
		"password_reset":     func(s string) error { _, err := domain.ParseRevokeReason(s); return err },
		"google":             func(s string) error { _, err := domain.ParseIdentityProviderName(s); return err },
		"login":              func(s string) error { _, err := domain.ParseOIDCPurpose(s); return err },
		"link":               func(s string) error { _, err := domain.ParseOIDCPurpose(s); return err },
		"email_verification": func(s string) error { _, err := domain.ParseOTPPurpose(s); return err },
	}
	for value, parse := range ok {
		if err := parse(value); err != nil {
			t.Errorf("parse(%q) error = %v", value, err)
		}
		if err := parse(strings.ToUpper(value)); err == nil {
			t.Errorf("parse(%q) accepted a different case", strings.ToUpper(value))
		}
		if err := parse(" " + value); err == nil {
			t.Errorf("parse(%q) accepted surrounding space", " "+value)
		}
	}
	_, err := domain.ParseOTPPurpose("login")
	requireInvalidInput(t, err, "purpose")
	if !strings.Contains(err.Error(), "email_verification, password_reset") {
		t.Errorf("enum error %q does not list the allowed values", err)
	}
}

func validSession(t *testing.T) domain.Session {
	return domain.Session{ID: mustID(t), UserID: mustID(t), TokenHash: hash32, CreatedAt: t0, ExpiresAt: t0.Add(7 * 24 * time.Hour)}
}

func TestNewSession(t *testing.T) {
	if _, err := domain.NewSession(validSession(t)); err != nil {
		t.Fatalf("NewSession(valid) error = %v", err)
	}
	bad := map[string]func(*domain.Session){
		"no id":            func(s *domain.Session) { s.ID = domain.ID{} },
		"no user":          func(s *domain.Session) { s.UserID = domain.ID{} },
		"short hash":       func(s *domain.Session) { s.TokenHash = hash31 },
		"nil hash":         func(s *domain.Session) { s.TokenHash = nil },
		"no created_at":    func(s *domain.Session) { s.CreatedAt = time.Time{} },
		"expires at start": func(s *domain.Session) { s.ExpiresAt = s.CreatedAt },
		"already revoked":  func(s *domain.Session) { s.RevokedAt = &revoked },
		"has reason":       func(s *domain.Session) { s.RevokeReason = domain.RevokeReasonLogout },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			s := validSession(t)
			mutate(&s)
			_, err := domain.NewSession(s)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}

func TestNewAuthProvider(t *testing.T) {
	valid := func() domain.AuthProvider {
		return domain.AuthProvider{ID: mustID(t), UserID: mustID(t), Provider: domain.IdentityProviderGoogle, ProviderSubject: "1098765432101234567890", CreatedAt: t0}
	}
	if _, err := domain.NewAuthProvider(valid()); err != nil {
		t.Fatalf("NewAuthProvider(valid) error = %v", err)
	}
	p := valid()
	p.ProviderSubject = strings.Repeat("9", 255)
	if _, err := domain.NewAuthProvider(p); err != nil {
		t.Errorf("255-character subject rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.AuthProvider){
		"no user":       func(p *domain.AuthProvider) { p.UserID = domain.ID{} },
		"bad provider":  func(p *domain.AuthProvider) { p.Provider = "github" },
		"empty subject": func(p *domain.AuthProvider) { p.ProviderSubject = "" },
		"long subject":  func(p *domain.AuthProvider) { p.ProviderSubject = strings.Repeat("9", 256) },
		"control":       func(p *domain.AuthProvider) { p.ProviderSubject = "12\n34" },
		"no created_at": func(p *domain.AuthProvider) { p.CreatedAt = time.Time{} },
	} {
		p := valid()
		mutate(&p)
		if _, err := domain.NewAuthProvider(p); err == nil {
			t.Errorf("%s: NewAuthProvider accepted an invalid link", name)
		}
	}
}

func TestNewOIDCFlowUserRule(t *testing.T) {
	flow := func(purpose domain.OIDCPurpose, user domain.ID) domain.OIDCFlow {
		return domain.OIDCFlow{ID: mustID(t), Purpose: purpose, UserID: user, StateHash: hash32, NonceHash: hash32,
			BrowserBindingHash: hash32, CreatedAt: t0, ExpiresAt: t0.Add(10 * time.Minute)}
	}
	if _, err := domain.NewOIDCFlow(flow(domain.OIDCPurposeLogin, domain.ID{})); err != nil {
		t.Errorf("login flow without user rejected: %v", err)
	}
	if _, err := domain.NewOIDCFlow(flow(domain.OIDCPurposeLink, mustID(t))); err != nil {
		t.Errorf("link flow with user rejected: %v", err)
	}
	for name, f := range map[string]domain.OIDCFlow{
		"login with user":   flow(domain.OIDCPurposeLogin, mustID(t)),
		"link without user": flow(domain.OIDCPurposeLink, domain.ID{}),
		"unknown purpose":   flow("signup", domain.ID{}),
		"short nonce hash": func() domain.OIDCFlow {
			f := flow(domain.OIDCPurposeLogin, domain.ID{})
			f.NonceHash = hash31
			return f
		}(),
		"already consumed": func() domain.OIDCFlow {
			f := flow(domain.OIDCPurposeLogin, domain.ID{})
			f.ConsumedAt = &revoked
			return f
		}(),
		"expires before now": func() domain.OIDCFlow {
			f := flow(domain.OIDCPurposeLogin, domain.ID{})
			f.ExpiresAt = t0.Add(-time.Second)
			return f
		}(),
	} {
		_, err := domain.NewOIDCFlow(f)
		if err == nil {
			t.Errorf("%s: NewOIDCFlow accepted an invalid flow", name)
		}
	}
}

func TestNewOTPChallenge(t *testing.T) {
	valid := func() domain.OTPChallenge {
		return domain.OTPChallenge{ID: mustID(t), UserID: mustID(t), Purpose: domain.OTPPurposePasswordReset, OTPHash: hash32, CreatedAt: t0, ExpiresAt: t0.Add(10 * time.Minute)}
	}
	if _, err := domain.NewOTPChallenge(valid()); err != nil {
		t.Fatalf("NewOTPChallenge(valid) error = %v", err)
	}
	for name, mutate := range map[string]func(*domain.OTPChallenge){
		"unknown purpose":  func(c *domain.OTPChallenge) { c.Purpose = "login" },
		"failed attempts":  func(c *domain.OTPChallenge) { c.FailedAttempts = 1 },
		"consumed":         func(c *domain.OTPChallenge) { c.ConsumedAt = &revoked },
		"invalidated":      func(c *domain.OTPChallenge) { c.InvalidatedAt = &revoked },
		"short hash":       func(c *domain.OTPChallenge) { c.OTPHash = hash31 },
		"expires at start": func(c *domain.OTPChallenge) { c.ExpiresAt = c.CreatedAt },
	} {
		c := valid()
		mutate(&c)
		if _, err := domain.NewOTPChallenge(c); err == nil {
			t.Errorf("%s: NewOTPChallenge accepted an invalid challenge", name)
		}
	}
	if domain.MaxOTPFailedAttemptsCeiling != 20 {
		t.Errorf("MaxOTPFailedAttemptsCeiling = %d, want the DB CHECK ceiling 20", domain.MaxOTPFailedAttemptsCeiling)
	}
}
