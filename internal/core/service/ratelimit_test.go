package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"net/netip"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func newTestPolicy(t *testing.T) *RateLimitPolicy {
	t.Helper()
	keys, err := NewRateLimitKeys([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewRateLimitPolicy(keys, RateLimitConfig{
		OTPResendCooldown: time.Minute, OTPRequestsPerHourEmail: 5, OTPRequestsPerHourIP: 20,
		LoginAttemptsPerEmail: 10, LoginAttemptsPerIP: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

type ruleShape struct {
	limit  int
	window time.Duration
}

// shapes groups rules by scope; a scope with two rules (cooldown and hourly) lists both.
func shapes(rules []domain.RateLimitRule) map[domain.RateLimitScope][]ruleShape {
	out := map[domain.RateLimitScope][]ruleShape{}
	for _, r := range rules {
		out[r.Scope] = append(out[r.Scope], ruleShape{r.Limit, r.Window})
	}
	return out
}

func TestRateLimitKeyFormat(t *testing.T) {
	secret := []byte(strings.Repeat("k", 32))
	keys, err := NewRateLimitKeys(secret)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("login_email\x00user@example.com"))
	if got := keys.Key(domain.RateLimitLoginEmail, "user@example.com"); !bytes.Equal(got, mac.Sum(nil)) || len(got) != domain.HashSize {
		t.Errorf("Key() = %x, want HMAC-SHA-256(secret, scope ‖ 0x00 ‖ value)", got)
	}

	a := keys.Key(domain.RateLimitLoginEmail, "user@example.com")
	if b := keys.Key(domain.RateLimitSignupIP, "user@example.com"); bytes.Equal(a, b) {
		t.Error("the same value under two scopes must give two keys")
	}
	other, err := NewRateLimitKeys([]byte(strings.Repeat("j", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, other.Key(domain.RateLimitLoginEmail, "user@example.com")) {
		t.Error("a different secret must give a different key")
	}
}

func TestRateLimitPolicyRules(t *testing.T) {
	policy := newTestPolicy(t)
	email := mustEmail(t, "user@example.com")
	ip := netip.MustParseAddr("203.0.113.9")
	hour, quarter := time.Hour, 15*time.Minute

	cooldownAndHourly := []ruleShape{{1, time.Minute}, {5, hour}}

	tests := []struct {
		name  string
		build func() ([]domain.RateLimitRule, error)
		want  map[domain.RateLimitScope][]ruleShape
	}{
		{"login", func() ([]domain.RateLimitRule, error) { return policy.Login(email, ip) },
			map[domain.RateLimitScope][]ruleShape{
				domain.RateLimitLoginIP:    {{30, quarter}},
				domain.RateLimitLoginEmail: {{10, quarter}},
			}},
		{"signup", func() ([]domain.RateLimitRule, error) { return policy.Signup(email, ip) },
			map[domain.RateLimitScope][]ruleShape{
				domain.RateLimitSignupIP:                      {{20, hour}},
				domain.RateLimitEmailVerificationRequestEmail: cooldownAndHourly,
			}},
		{"email verification request", func() ([]domain.RateLimitRule, error) { return policy.EmailVerificationRequest(email, ip) },
			map[domain.RateLimitScope][]ruleShape{
				domain.RateLimitEmailVerificationRequestIP:    {{20, hour}},
				domain.RateLimitEmailVerificationRequestEmail: cooldownAndHourly,
			}},
		{"email verification confirm", func() ([]domain.RateLimitRule, error) { return policy.EmailVerificationConfirm(ip) },
			map[domain.RateLimitScope][]ruleShape{domain.RateLimitEmailVerificationConfirmIP: {{20, quarter}}}},
		{"google start", func() ([]domain.RateLimitRule, error) { return policy.GoogleStart(ip) },
			map[domain.RateLimitScope][]ruleShape{domain.RateLimitGoogleStartIP: {{20, hour}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules, err := tt.build()
			if err != nil {
				t.Fatal(err)
			}
			got := shapes(rules)
			if len(got) != len(tt.want) {
				t.Fatalf("scopes = %v, want %v", got, tt.want)
			}
			for scope, want := range tt.want {
				if len(got[scope]) != len(want) {
					t.Fatalf("%s rules = %v, want %v", scope, got[scope], want)
				}
				for i := range want {
					if got[scope][i] != want[i] {
						t.Errorf("%s rule %d = %v, want %v", scope, i, got[scope][i], want[i])
					}
				}
			}
			for _, r := range rules {
				if _, err := domain.NewRateLimitRule(r); err != nil {
					t.Errorf("rule for %s is not valid: %v", r.Scope, err)
				}
			}
		})
	}
}

func TestRateLimitPolicyEmailRulesShareOneKey(t *testing.T) {
	policy := newTestPolicy(t)
	rules, err := policy.Signup(mustEmail(t, "user@example.com"), netip.MustParseAddr("203.0.113.9"))
	if err != nil {
		t.Fatal(err)
	}
	var emailKeys [][]byte
	for _, r := range rules {
		if r.Scope == domain.RateLimitEmailVerificationRequestEmail {
			emailKeys = append(emailKeys, r.KeyHash)
		}
	}
	if len(emailKeys) != 2 || !bytes.Equal(emailKeys[0], emailKeys[1]) {
		t.Errorf("the cooldown and the hourly rule must count the same events: keys = %x", emailKeys)
	}
}

func TestRateLimitPolicyKeysHideEmailAndNormaliseIP(t *testing.T) {
	policy := newTestPolicy(t)
	email := mustEmail(t, "user@example.com")

	plain, err := policy.EmailVerificationConfirm(netip.MustParseAddr("203.0.113.9"))
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := policy.EmailVerificationConfirm(netip.MustParseAddr("::ffff:203.0.113.9"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain[0].KeyHash, mapped[0].KeyHash) {
		t.Error("an IPv4 address in IPv6 notation must share the IPv4 address's quota")
	}
	neighbour, err := policy.EmailVerificationConfirm(netip.MustParseAddr("203.0.113.10"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(plain[0].KeyHash, neighbour[0].KeyHash) {
		t.Error("the full address is the key: a neighbour must not share the quota")
	}

	rules, err := policy.Login(email, netip.MustParseAddr("203.0.113.9"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if bytes.Contains(r.KeyHash, []byte("user@example.com")) || bytes.Contains(r.KeyHash, []byte("203.0.113.9")) {
			t.Errorf("key for %s contains the raw value", r.Scope)
		}
	}
}

func TestRateLimitPolicyRejectsUnknownClientIP(t *testing.T) {
	policy := newTestPolicy(t)
	_, err := policy.Login(mustEmail(t, "user@example.com"), netip.Addr{})
	requireCode(t, err, domain.CodeInternal)
}

func TestNewRateLimitPolicyValidatesConfig(t *testing.T) {
	keys, err := NewRateLimitKeys([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	good := RateLimitConfig{
		OTPResendCooldown: time.Minute, OTPRequestsPerHourEmail: 5, OTPRequestsPerHourIP: 20,
		LoginAttemptsPerEmail: 10, LoginAttemptsPerIP: 30,
	}
	for name, mutate := range map[string]func(*RateLimitConfig){
		"cooldown":    func(c *RateLimitConfig) { c.OTPResendCooldown = 0 },
		"otp email":   func(c *RateLimitConfig) { c.OTPRequestsPerHourEmail = 0 },
		"otp ip":      func(c *RateLimitConfig) { c.OTPRequestsPerHourIP = -1 },
		"login email": func(c *RateLimitConfig) { c.LoginAttemptsPerEmail = 0 },
		"login ip":    func(c *RateLimitConfig) { c.LoginAttemptsPerIP = 0 },
	} {
		cfg := good
		mutate(&cfg)
		if _, err := NewRateLimitPolicy(keys, cfg); err == nil {
			t.Errorf("%s: NewRateLimitPolicy() accepted a non-positive value", name)
		}
	}
	if _, err := NewRateLimitPolicy(nil, good); err == nil {
		t.Error("NewRateLimitPolicy(nil keys) accepted")
	}
	if _, err := NewRateLimitKeys([]byte("short")); err == nil {
		t.Error("NewRateLimitKeys() accepted a secret under 32 bytes")
	}
}
