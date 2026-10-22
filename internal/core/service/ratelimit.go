package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/netip"
	"time"

	"go-backend-stock/internal/core/domain"
)

// Limits that are fixed in phase 1 rather than configurable (phase 1 spec section 10).
const (
	loginWindow             = 15 * time.Minute
	hourWindow              = time.Hour
	signupPerHourIP         = 20
	googleStartPerHourIP    = 20
	verificationConfirmIP   = 20 // per loginWindow
	verificationConfirmSpan = loginWindow
)

// RateLimitKeys turns an email or an IP address into the opaque key stored in rate_limit_keys, so
// the table never holds a raw email or IP.
type RateLimitKeys struct{ secret []byte }

// NewRateLimitKeys returns keys derived with secret (at least 32 bytes).
func NewRateLimitKeys(secret []byte) (*RateLimitKeys, error) {
	s, err := copySecret("rate limit secret", secret)
	if err != nil {
		return nil, err
	}
	return &RateLimitKeys{secret: s}, nil
}

// Key returns HMAC-SHA-256(secret, scope ‖ 0x00 ‖ value). value is a normalised email or a full
// client IP.
func (k *RateLimitKeys) Key(scope domain.RateLimitScope, value string) []byte {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write([]byte(string(scope) + "\x00" + value))
	return mac.Sum(nil)
}

// RateLimitConfig holds the configurable limits (OTP_RESEND_COOLDOWN, OTP_REQUESTS_PER_HOUR_*,
// LOGIN_ATTEMPTS_PER_*). All values must be positive.
type RateLimitConfig struct {
	OTPResendCooldown       time.Duration
	OTPRequestsPerHourEmail int
	OTPRequestsPerHourIP    int
	LoginAttemptsPerEmail   int
	LoginAttemptsPerIP      int
}

// RateLimitPolicy builds the rules each endpoint passes to RateLimitRepository.Admit. Every rule
// goes through domain.NewRateLimitRule, so none can have a zero limit.
type RateLimitPolicy struct {
	keys *RateLimitKeys
	cfg  RateLimitConfig
}

// NewRateLimitPolicy checks cfg and returns the policy.
func NewRateLimitPolicy(keys *RateLimitKeys, cfg RateLimitConfig) (*RateLimitPolicy, error) {
	switch {
	case keys == nil:
		return nil, errors.New("rate limit policy needs keys")
	case cfg.OTPResendCooldown <= 0, cfg.OTPRequestsPerHourEmail < 1, cfg.OTPRequestsPerHourIP < 1,
		cfg.LoginAttemptsPerEmail < 1, cfg.LoginAttemptsPerIP < 1:
		return nil, errors.New("rate limits must be positive")
	}
	return &RateLimitPolicy{keys: keys, cfg: cfg}, nil
}

// Login limits password login by client IP and by account email.
func (p *RateLimitPolicy) Login(email domain.Email, ip netip.Addr) ([]domain.RateLimitRule, error) {
	return p.build(
		p.spec(domain.RateLimitLoginIP, ipValue(ip), p.cfg.LoginAttemptsPerIP, loginWindow),
		p.spec(domain.RateLimitLoginEmail, email.String(), p.cfg.LoginAttemptsPerEmail, loginWindow),
	)
}

// Signup limits registration by client IP, and counts it against the email's verification-code
// quota so register followed by "send again" cannot beat the cooldown.
func (p *RateLimitPolicy) Signup(email domain.Email, ip netip.Addr) ([]domain.RateLimitRule, error) {
	return p.build(append(p.verificationEmailSpecs(email),
		p.spec(domain.RateLimitSignupIP, ipValue(ip), signupPerHourIP, hourWindow))...)
}

// EmailVerificationRequest limits asking for a new verification code.
func (p *RateLimitPolicy) EmailVerificationRequest(email domain.Email, ip netip.Addr) ([]domain.RateLimitRule, error) {
	return p.build(append(p.verificationEmailSpecs(email),
		p.spec(domain.RateLimitEmailVerificationRequestIP, ipValue(ip), p.cfg.OTPRequestsPerHourIP, hourWindow))...)
}

// EmailVerificationConfirm limits guessing codes from one client IP.
func (p *RateLimitPolicy) EmailVerificationConfirm(ip netip.Addr) ([]domain.RateLimitRule, error) {
	return p.build(p.spec(domain.RateLimitEmailVerificationConfirmIP, ipValue(ip), verificationConfirmIP, verificationConfirmSpan))
}

// GoogleStart limits starting a Google sign-in from one client IP.
func (p *RateLimitPolicy) GoogleStart(ip netip.Addr) ([]domain.RateLimitRule, error) {
	return p.build(p.spec(domain.RateLimitGoogleStartIP, ipValue(ip), googleStartPerHourIP, hourWindow))
}

// verificationEmailSpecs are the two rules on one email key: at most one code per cooldown and at
// most OTPRequestsPerHourEmail per hour.
func (p *RateLimitPolicy) verificationEmailSpecs(email domain.Email) []ruleSpec {
	return []ruleSpec{
		p.spec(domain.RateLimitEmailVerificationRequestEmail, email.String(), 1, p.cfg.OTPResendCooldown),
		p.spec(domain.RateLimitEmailVerificationRequestEmail, email.String(), p.cfg.OTPRequestsPerHourEmail, hourWindow),
	}
}

type ruleSpec struct {
	scope  domain.RateLimitScope
	value  string
	limit  int
	window time.Duration
}

func (p *RateLimitPolicy) spec(scope domain.RateLimitScope, value string, limit int, window time.Duration) ruleSpec {
	return ruleSpec{scope: scope, value: value, limit: limit, window: window}
}

func (p *RateLimitPolicy) build(specs ...ruleSpec) ([]domain.RateLimitRule, error) {
	rules := make([]domain.RateLimitRule, 0, len(specs))
	for _, s := range specs {
		if s.value == "" {
			return nil, domain.NewError(domain.CodeInternal, "rate limit needs a client IP")
		}
		rule, err := domain.NewRateLimitRule(domain.RateLimitRule{
			Scope: s.scope, KeyHash: p.keys.Key(s.scope, s.value), Limit: s.limit, Window: s.window,
		})
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// ipValue is the full address, with an IPv4 address in IPv6 notation turned back into IPv4 so
// both spellings share one quota. It is "" for the zero Addr.
func ipValue(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	return ip.Unmap().WithZone("").String()
}
