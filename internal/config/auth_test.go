package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func devSecrets() map[string]string {
	return map[string]string{
		"CSRF_SECRET":       "dev-only-csrf-secret-0123456789abcdef",
		"OTP_SECRET":        "dev-only-otp-secret-0123456789abcdef",
		"RATE_LIMIT_SECRET": "dev-only-rate-limit-secret-0123456789",
		"OIDC_SECRET":       "dev-only-oidc-secret-0123456789abcdef",
	}
}

func validProdEnv() map[string]string {
	return map[string]string{
		"APP_ENV":           "production",
		"PUBLIC_ORIGIN":     "https://stock.example.com",
		"DATABASE_URL":      "postgres://app_runtime:prodpass@db.example.com:5432/postgres?sslmode=verify-full",
		"CSRF_SECRET":       strings.Repeat("c", 32),
		"OTP_SECRET":        strings.Repeat("o", 32),
		"RATE_LIMIT_SECRET": strings.Repeat("r", 32),
		"OIDC_SECRET":       strings.Repeat("i", 32),
		"SMTP_HOST":         "smtp.example.com",
		"SMTP_PORT":         "587",
		"SMTP_FROM":         "no-reply@example.com",
		"SMTP_TLS":          "starttls",
	}
}

func TestLoadAuthDefaults(t *testing.T) {
	cfg, err := Load(envFrom(validDevEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	a := cfg.Auth
	if a.CSRFSecret.Reveal() != devSecrets()["CSRF_SECRET"] || a.OTPSecret.Reveal() != devSecrets()["OTP_SECRET"] ||
		a.RateLimitSecret.Reveal() != devSecrets()["RATE_LIMIT_SECRET"] || a.OIDCSecret.Reveal() != devSecrets()["OIDC_SECRET"] {
		t.Error("secrets were not loaded")
	}
	if a.SessionTTL != 168*time.Hour || a.PasswordHashConcurrency != 4 ||
		a.ResponseFloor != 500*time.Millisecond || a.ResponseJitter != 100*time.Millisecond ||
		a.OTPMaxAttempts != 5 || a.OTPResendCooldown != time.Minute ||
		a.OTPRequestsPerHourEmail != 5 || a.OTPRequestsPerHourIP != 20 ||
		a.LoginAttemptsPerEmail != 10 || a.LoginAttemptsPerIP != 30 || len(a.TrustedProxies) != 0 {
		t.Errorf("auth defaults = %+v", a)
	}
	s := cfg.SMTP
	if s.Host != "mailpit" || s.Port != 1025 || s.Username != "" || s.Password != "" ||
		s.From != "no-reply@localhost" || s.TLS != SMTPTLSNone || s.Timeout != 10*time.Second {
		t.Errorf("smtp defaults = %+v", s)
	}
}

func TestLoadAuthOverrides(t *testing.T) {
	env := validDevEnv()
	for k, v := range map[string]string{
		"SESSION_TTL": "24h", "PASSWORD_HASH_CONCURRENCY": "8", "AUTH_RESPONSE_FLOOR": "0s", "AUTH_RESPONSE_JITTER": "0s",
		"OTP_MAX_ATTEMPTS": "3", "OTP_RESEND_COOLDOWN": "2m", "OTP_REQUESTS_PER_HOUR_EMAIL": "3", "OTP_REQUESTS_PER_HOUR_IP": "10",
		"LOGIN_ATTEMPTS_PER_EMAIL": "4", "LOGIN_ATTEMPTS_PER_IP": "12", "TRUSTED_PROXY_CIDRS": "172.16.0.0/12",
		"SMTP_HOST": "smtp.example.com", "SMTP_PORT": "587", "SMTP_USERNAME": "mailer", "SMTP_PASSWORD": "mail-pass",
		"SMTP_FROM": "noreply@example.com", "SMTP_TLS": "STARTTLS", "MAIL_SEND_TIMEOUT": "20s",
	} {
		env[k] = v
	}
	cfg, err := Load(envFrom(env))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	a, s := cfg.Auth, cfg.SMTP
	if a.SessionTTL != 24*time.Hour || a.PasswordHashConcurrency != 8 || a.ResponseFloor != 0 || a.ResponseJitter != 0 ||
		a.OTPMaxAttempts != 3 || a.OTPResendCooldown != 2*time.Minute || a.OTPRequestsPerHourEmail != 3 ||
		a.OTPRequestsPerHourIP != 10 || a.LoginAttemptsPerEmail != 4 || a.LoginAttemptsPerIP != 12 {
		t.Errorf("auth overrides = %+v", a)
	}
	if len(a.TrustedProxies) != 1 || a.TrustedProxies[0] != netip.MustParsePrefix("172.16.0.0/12") {
		t.Errorf("TrustedProxies = %v", a.TrustedProxies)
	}
	if s.Host != "smtp.example.com" || s.Port != 587 || s.Username != "mailer" || s.Password.Reveal() != "mail-pass" ||
		s.From != "noreply@example.com" || s.TLS != SMTPTLSStartTLS || s.Timeout != 20*time.Second {
		t.Errorf("smtp overrides = %+v", s)
	}
}

func TestLoadAuthRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{"missing csrf secret", func(e map[string]string) { delete(e, "CSRF_SECRET") }, "CSRF_SECRET is required"},
		{"short otp secret", func(e map[string]string) { e["OTP_SECRET"] = strings.Repeat("x", 31) }, "OTP_SECRET must be at least 32 bytes"},
		{"missing rate limit secret", func(e map[string]string) { delete(e, "RATE_LIMIT_SECRET") }, "RATE_LIMIT_SECRET is required"},
		{"missing oidc secret", func(e map[string]string) { delete(e, "OIDC_SECRET") }, "OIDC_SECRET is required"},
		{"session ttl too short", func(e map[string]string) { e["SESSION_TTL"] = "30m" }, "SESSION_TTL"},
		{"session ttl too long", func(e map[string]string) { e["SESSION_TTL"] = "721h" }, "SESSION_TTL"},
		{"hash concurrency zero", func(e map[string]string) { e["PASSWORD_HASH_CONCURRENCY"] = "0" }, "PASSWORD_HASH_CONCURRENCY"},
		{"hash concurrency 65", func(e map[string]string) { e["PASSWORD_HASH_CONCURRENCY"] = "65" }, "PASSWORD_HASH_CONCURRENCY"},
		{"floor too long", func(e map[string]string) { e["AUTH_RESPONSE_FLOOR"] = "6s" }, "AUTH_RESPONSE_FLOOR"},
		{"jitter too long", func(e map[string]string) { e["AUTH_RESPONSE_JITTER"] = "2s" }, "AUTH_RESPONSE_JITTER"},
		{"negative jitter", func(e map[string]string) { e["AUTH_RESPONSE_JITTER"] = "-1s" }, "AUTH_RESPONSE_JITTER"},
		{"otp attempts zero", func(e map[string]string) { e["OTP_MAX_ATTEMPTS"] = "0" }, "OTP_MAX_ATTEMPTS"},
		{"otp attempts above the database ceiling", func(e map[string]string) { e["OTP_MAX_ATTEMPTS"] = "21" }, "OTP_MAX_ATTEMPTS"},
		{"cooldown zero", func(e map[string]string) { e["OTP_RESEND_COOLDOWN"] = "0s" }, "OTP_RESEND_COOLDOWN"},
		{"cooldown two hours", func(e map[string]string) { e["OTP_RESEND_COOLDOWN"] = "2h" }, "OTP_RESEND_COOLDOWN"},
		{"otp per hour email zero", func(e map[string]string) { e["OTP_REQUESTS_PER_HOUR_EMAIL"] = "0" }, "OTP_REQUESTS_PER_HOUR_EMAIL"},
		{"otp per hour ip zero", func(e map[string]string) { e["OTP_REQUESTS_PER_HOUR_IP"] = "0" }, "OTP_REQUESTS_PER_HOUR_IP"},
		{"login per email zero", func(e map[string]string) { e["LOGIN_ATTEMPTS_PER_EMAIL"] = "0" }, "LOGIN_ATTEMPTS_PER_EMAIL"},
		{"login per ip zero", func(e map[string]string) { e["LOGIN_ATTEMPTS_PER_IP"] = "0" }, "LOGIN_ATTEMPTS_PER_IP"},
		{"cidr without mask", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "10.0.0.1" }, "TRUSTED_PROXY_CIDRS"},
		{"cidr trusting everything", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "0.0.0.0/0" }, "TRUSTED_PROXY_CIDRS"},
		{"ipv6 cidr trusting everything", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "::/0" }, "TRUSTED_PROXY_CIDRS"},
		{"one bad cidr in a list", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8,garbage" }, "TRUSTED_PROXY_CIDRS"},
		{"ipv4-mapped cidr never matches", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "::ffff:10.0.0.0/104" }, "TRUSTED_PROXY_CIDRS"},
		{"smtp port zero", func(e map[string]string) { e["SMTP_PORT"] = "0" }, "SMTP_PORT"},
		{"smtp port too large", func(e map[string]string) { e["SMTP_PORT"] = "70000" }, "SMTP_PORT"},
		{"smtp port text", func(e map[string]string) { e["SMTP_PORT"] = "abc" }, "SMTP_PORT"},
		{"unknown smtp tls", func(e map[string]string) { e["SMTP_TLS"] = "ssl" }, "SMTP_TLS must be one of"},
		{"smtp from with a name", func(e map[string]string) { e["SMTP_FROM"] = "Stock <a@example.com>" }, "SMTP_FROM"},
		{"smtp from with a header", func(e map[string]string) { e["SMTP_FROM"] = "a@example.com\r\nBcc: b@example.com" }, "SMTP_FROM"},
		{"smtp from not an address", func(e map[string]string) { e["SMTP_FROM"] = "not-an-address" }, "SMTP_FROM"},
		{"smtp username without password", func(e map[string]string) { e["SMTP_USERNAME"] = "mailer" }, "SMTP_USERNAME and SMTP_PASSWORD"},
		{"smtp password without username", func(e map[string]string) { e["SMTP_PASSWORD"] = "pw" }, "SMTP_USERNAME and SMTP_PASSWORD"},
		{"mail timeout zero", func(e map[string]string) { e["MAIL_SEND_TIMEOUT"] = "0s" }, "MAIL_SEND_TIMEOUT"},
		{"mail timeout two minutes", func(e map[string]string) { e["MAIL_SEND_TIMEOUT"] = "2m" }, "MAIL_SEND_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validDevEnv()
			tt.mutate(env)
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadProductionAuthRules(t *testing.T) {
	if _, err := Load(envFrom(validProdEnv())); err != nil {
		t.Fatalf("Load(valid production) error = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{"development placeholder", func(e map[string]string) { e["CSRF_SECRET"] = "dev-only-" + strings.Repeat("x", 30) }, "CSRF_SECRET must not use a development placeholder"},
		{"duplicate secrets", func(e map[string]string) { e["OTP_SECRET"] = e["CSRF_SECRET"] }, "must all be different"},
		{"no smtp host", func(e map[string]string) { delete(e, "SMTP_HOST") }, "SMTP_HOST is required in production"},
		{"no smtp port", func(e map[string]string) { delete(e, "SMTP_PORT") }, "SMTP_PORT is required in production"},
		{"no smtp from", func(e map[string]string) { delete(e, "SMTP_FROM") }, "SMTP_FROM is required in production"},
		{"no smtp tls", func(e map[string]string) { delete(e, "SMTP_TLS") }, "SMTP_TLS is required in production"},
		{"smtp tls none", func(e map[string]string) { e["SMTP_TLS"] = "none" }, "SMTP_TLS=none is not allowed in production"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validProdEnv()
			tt.mutate(env)
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadDevelopmentAllowsSharedSecrets(t *testing.T) {
	env := validDevEnv()
	for _, k := range []string{"OTP_SECRET", "RATE_LIMIT_SECRET", "OIDC_SECRET"} {
		env[k] = env["CSRF_SECRET"]
	}
	if _, err := Load(envFrom(env)); err != nil {
		t.Errorf("development must accept repeated placeholder secrets, error = %v", err)
	}
}

func TestLoadTrustedProxyCIDRs(t *testing.T) {
	env := validDevEnv()
	env["TRUSTED_PROXY_CIDRS"] = "10.1.2.3/8, 192.168.1.0/24 ,2001:db8::/32"
	cfg, err := Load(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("2001:db8::/32")}
	if fmt.Sprint(cfg.Auth.TrustedProxies) != fmt.Sprint(want) {
		t.Errorf("TrustedProxies = %v, want %v (host bits cleared)", cfg.Auth.TrustedProxies, want)
	}
}

func TestLoadErrorsNeverEchoValues(t *testing.T) {
	env := validDevEnv()
	env["CSRF_SECRET"] = "short-ZQXJ"
	env["TRUSTED_PROXY_CIDRS"] = "ZQXJ"
	env["SMTP_FROM"] = "ZQXJ-not-an-address"
	env["SMTP_TLS"] = "ZQXJ"
	env["SMTP_PORT"] = "ZQXJ"
	_, err := Load(envFrom(env))
	if err == nil {
		t.Fatal("Load() accepted invalid values")
	}
	if strings.Contains(err.Error(), "ZQXJ") {
		t.Errorf("error echoes a configured value: %v", err)
	}
}

func TestConfigNeverPrintsSecrets(t *testing.T) {
	env := validProdEnv()
	env["DATABASE_URL"] = "postgres://app_runtime:S3cretDbPass@db.example.com:5432/postgres?sslmode=verify-full"
	env["SMTP_USERNAME"] = "mailer"
	env["SMTP_PASSWORD"] = "S3cretMailPass"
	cfg, err := Load(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{
		"S3cretDbPass", "S3cretMailPass",
		strings.Repeat("c", 32), strings.Repeat("o", 32), strings.Repeat("r", 32), strings.Repeat("i", 32),
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("config", slog.Any("config", cfg))
	log.Info("auth", slog.Any("auth", cfg.Auth), slog.Any("smtp", cfg.SMTP))
	log.Info("secret", slog.Any("secret", cfg.Auth.OTPSecret))
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	outputs := []string{
		buf.String(), string(data),
		fmt.Sprint(cfg), fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%+v", cfg.Auth), fmt.Sprintf("%+v", cfg.SMTP), fmt.Sprintf("%#v", cfg.Auth.CSRFSecret),
	}
	for _, out := range outputs {
		for _, secret := range secrets {
			if strings.Contains(out, secret) {
				t.Errorf("output leaks %q: %s", secret, out)
			}
		}
	}
	if cfg.Auth.CSRFSecret.Reveal() != strings.Repeat("c", 32) || len(cfg.Auth.CSRFSecret.Bytes()) != 32 {
		t.Error("Reveal/Bytes must still return the value")
	}
}
