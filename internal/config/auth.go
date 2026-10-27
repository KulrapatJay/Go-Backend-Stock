package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
	"strings"
	"time"

	"go-backend-stock/internal/core/domain"
)

const (
	minSecretBytes  = 32
	devSecretPrefix = "dev-only-"
	maxRateLimit    = 1000
)

// SMTP_TLS values.
const (
	SMTPTLSNone     = "none"
	SMTPTLSStartTLS = "starttls"
	SMTPTLSImplicit = "tls"
)

// AuthConfig holds the settings of registration, login, sessions and OTP.
type AuthConfig struct {
	CSRFSecret              Secret
	OTPSecret               Secret
	RateLimitSecret         Secret
	OIDCSecret              Secret
	SessionTTL              time.Duration
	PasswordHashConcurrency int
	ResponseFloor           time.Duration
	ResponseJitter          time.Duration
	TrustedProxies          []netip.Prefix
	OTPMaxAttempts          int
	OTPResendCooldown       time.Duration
	OTPRequestsPerHourEmail int
	OTPRequestsPerHourIP    int
	LoginAttemptsPerEmail   int
	LoginAttemptsPerIP      int
}

// SMTPConfig holds the settings of the mail server. TLS is one of the SMTPTLS* values.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password Secret
	From     string
	TLS      string
	Timeout  time.Duration
}

func loadAuth(getenv func(string) string, prod bool) (AuthConfig, []error) {
	var cfg AuthConfig
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	var err error

	secrets := []struct {
		name string
		dst  *Secret
	}{
		{"CSRF_SECRET", &cfg.CSRFSecret},
		{"OTP_SECRET", &cfg.OTPSecret},
		{"RATE_LIMIT_SECRET", &cfg.RateLimitSecret},
		{"OIDC_SECRET", &cfg.OIDCSecret},
	}
	for _, s := range secrets {
		*s.dst, err = parseSecret(s.name, getenv(s.name), prod)
		add(err)
	}
	if prod {
		seen := map[Secret]bool{}
		for _, s := range secrets {
			if *s.dst == "" {
				continue
			}
			if seen[*s.dst] {
				add(errors.New("CSRF_SECRET, OTP_SECRET, RATE_LIMIT_SECRET and OIDC_SECRET must all be different"))
				break
			}
			seen[*s.dst] = true
		}
	}

	cfg.SessionTTL, err = parseDurationInRange("SESSION_TTL", getenv("SESSION_TTL"), 168*time.Hour, time.Hour, 720*time.Hour)
	add(err)
	cfg.PasswordHashConcurrency, err = parseIntInRange("PASSWORD_HASH_CONCURRENCY", getenv("PASSWORD_HASH_CONCURRENCY"), 4, 1, 64)
	add(err)
	cfg.ResponseFloor, err = parseDurationInRange("AUTH_RESPONSE_FLOOR", getenv("AUTH_RESPONSE_FLOOR"), 500*time.Millisecond, 0, 5*time.Second)
	add(err)
	cfg.ResponseJitter, err = parseDurationInRange("AUTH_RESPONSE_JITTER", getenv("AUTH_RESPONSE_JITTER"), 100*time.Millisecond, 0, time.Second)
	add(err)
	cfg.TrustedProxies, err = parseCIDRs(getenv("TRUSTED_PROXY_CIDRS"))
	add(err)
	cfg.OTPMaxAttempts, err = parseIntInRange("OTP_MAX_ATTEMPTS", getenv("OTP_MAX_ATTEMPTS"), 5, 1, domain.MaxOTPFailedAttemptsCeiling)
	add(err)
	cfg.OTPResendCooldown, err = parseDurationInRange("OTP_RESEND_COOLDOWN", getenv("OTP_RESEND_COOLDOWN"), time.Minute, time.Second, time.Hour)
	add(err)
	cfg.OTPRequestsPerHourEmail, err = parseIntInRange("OTP_REQUESTS_PER_HOUR_EMAIL", getenv("OTP_REQUESTS_PER_HOUR_EMAIL"), 5, 1, maxRateLimit)
	add(err)
	cfg.OTPRequestsPerHourIP, err = parseIntInRange("OTP_REQUESTS_PER_HOUR_IP", getenv("OTP_REQUESTS_PER_HOUR_IP"), 20, 1, maxRateLimit)
	add(err)
	cfg.LoginAttemptsPerEmail, err = parseIntInRange("LOGIN_ATTEMPTS_PER_EMAIL", getenv("LOGIN_ATTEMPTS_PER_EMAIL"), 10, 1, maxRateLimit)
	add(err)
	cfg.LoginAttemptsPerIP, err = parseIntInRange("LOGIN_ATTEMPTS_PER_IP", getenv("LOGIN_ATTEMPTS_PER_IP"), 30, 1, maxRateLimit)
	add(err)
	return cfg, errs
}

func loadSMTP(getenv func(string) string, prod bool) (SMTPConfig, []error) {
	var cfg SMTPConfig
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	var err error

	cfg.Host = strings.TrimSpace(getenv("SMTP_HOST"))
	if cfg.Host == "" {
		if prod {
			add(errors.New("SMTP_HOST is required in production"))
		} else {
			cfg.Host = "mailpit"
		}
	}
	if prod && strings.TrimSpace(getenv("SMTP_PORT")) == "" {
		add(errors.New("SMTP_PORT is required in production"))
	}
	cfg.Port, err = parseIntInRange("SMTP_PORT", getenv("SMTP_PORT"), 1025, 1, 65535)
	add(err)

	cfg.Username = strings.TrimSpace(getenv("SMTP_USERNAME"))
	cfg.Password = Secret(getenv("SMTP_PASSWORD"))
	if (cfg.Username == "") != (cfg.Password == "") {
		add(errors.New("SMTP_USERNAME and SMTP_PASSWORD must be set together"))
	}

	from := strings.TrimSpace(getenv("SMTP_FROM"))
	switch {
	case from == "" && prod:
		add(errors.New("SMTP_FROM is required in production"))
	case from == "":
		cfg.From = "no-reply@localhost"
	default:
		addr, parseErr := mail.ParseAddress(from)
		if parseErr != nil || addr.Address != from || strings.ContainsAny(from, "\r\n") {
			add(errors.New("SMTP_FROM must be a plain email address such as no-reply@example.com"))
		} else {
			cfg.From = from
		}
	}

	switch mode := strings.ToLower(strings.TrimSpace(getenv("SMTP_TLS"))); mode {
	case "":
		if prod {
			add(errors.New("SMTP_TLS is required in production and must be starttls or tls"))
		} else {
			cfg.TLS = SMTPTLSNone
		}
	case SMTPTLSNone:
		if prod {
			add(errors.New("SMTP_TLS=none is not allowed in production"))
		} else {
			cfg.TLS = SMTPTLSNone
		}
	case SMTPTLSStartTLS, SMTPTLSImplicit:
		cfg.TLS = mode
	default:
		add(errors.New("SMTP_TLS must be one of none, starttls, tls"))
	}

	cfg.Timeout, err = parseDurationInRange("MAIL_SEND_TIMEOUT", getenv("MAIL_SEND_TIMEOUT"), 10*time.Second, time.Second, time.Minute)
	add(err)
	return cfg, errs
}

// parseSecret never puts the value in its errors.
func parseSecret(name, raw string, prod bool) (Secret, error) {
	v := strings.TrimSpace(raw)
	switch {
	case v == "":
		return "", fmt.Errorf("%s is required", name)
	case len(v) < minSecretBytes:
		return "", fmt.Errorf("%s must be at least %d bytes", name, minSecretBytes)
	case prod && strings.HasPrefix(v, devSecretPrefix):
		return "", fmt.Errorf("%s must not use a development placeholder in production", name)
	}
	return Secret(v), nil
}

// parseCIDRs reads a comma-separated list of CIDR ranges. A range that trusts every address
// (/0) is refused: it would let any client choose the address that rate limits are keyed on.
func parseCIDRs(raw string) ([]netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		// An IPv4-mapped prefix (::ffff:10.0.0.0/104) never matches: client addresses are unmapped
		// before the comparison. Refuse it rather than silently trusting nothing.
		if err != nil || prefix.Bits() == 0 || prefix.Addr().Is4In6() {
			return nil, errors.New("TRUSTED_PROXY_CIDRS must be a comma-separated list of CIDR ranges such as 10.0.0.0/8, and must not trust every address")
		}
		out = append(out, prefix.Masked())
	}
	return out, nil
}
