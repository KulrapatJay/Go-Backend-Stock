package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	httpadapter "go-backend-stock/internal/adapter/http"
	"go-backend-stock/internal/adapter/identity"
	"go-backend-stock/internal/adapter/mail"
	"go-backend-stock/internal/adapter/postgres"
	"go-backend-stock/internal/config"
	"go-backend-stock/internal/core/port"
	"go-backend-stock/internal/core/service"
)

// googleTimeout limits each call to Google (discovery, token, keys).
const googleTimeout = 10 * time.Second

// systemClock is the real port.Clock.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// secureCookies reports whether the API is served over HTTPS, which turns on Secure and the
// __Host- cookie names (dev over plain HTTP keeps them off).
func secureCookies(publicOrigin string) bool { return strings.HasPrefix(publicOrigin, "https://") }

// smtpTLSMode maps the validated SMTP_TLS value to the mail adapter's mode.
func smtpTLSMode(v string) (mail.TLSMode, error) {
	switch v {
	case config.SMTPTLSNone:
		return mail.TLSNone, nil
	case config.SMTPTLSStartTLS:
		return mail.TLSStartTLS, nil
	case config.SMTPTLSImplicit:
		return mail.TLSImplicit, nil
	}
	return "", fmt.Errorf("unknown SMTP_TLS value %q", v)
}

// authParts is what buildAuth assembles: the routes' options, and the mail queue that main closes
// after the HTTP server.
type authParts struct {
	options httpadapter.AuthOptions
	mail    *mail.Dispatcher
}

// buildAuth wires the auth use cases from validated configuration. It opens no connection. It is
// the one place that guarantees every AuthOptions dependency is set.
func buildAuth(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (authParts, error) {
	fail := func(what string, err error) (authParts, error) {
		return authParts{}, fmt.Errorf("build auth: %s: %w", what, err)
	}
	store := postgres.NewStore(pool)
	clock := systemClock{}

	hasher, err := service.NewPasswordHasher(service.PasswordHasherConfig{
		Params: service.DefaultArgon2Params(), Concurrency: cfg.Auth.PasswordHashConcurrency, Logger: log,
	})
	if err != nil {
		return fail("password hasher", err)
	}
	blocklist, err := service.DefaultBlocklist()
	if err != nil {
		return fail("password blocklist", err)
	}
	passwords, err := service.NewPasswordPolicy(blocklist)
	if err != nil {
		return fail("password policy", err)
	}
	otp, err := service.NewOTPCodec(cfg.Auth.OTPSecret.Bytes())
	if err != nil {
		return fail("otp codec", err)
	}
	csrf, err := service.NewCSRFCodec(cfg.Auth.CSRFSecret.Bytes(), clock)
	if err != nil {
		return fail("csrf codec", err)
	}
	keys, err := service.NewRateLimitKeys(cfg.Auth.RateLimitSecret.Bytes())
	if err != nil {
		return fail("rate-limit keys", err)
	}
	limits, err := service.NewRateLimitPolicy(keys, service.RateLimitConfig{
		OTPResendCooldown:       cfg.Auth.OTPResendCooldown,
		OTPRequestsPerHourEmail: cfg.Auth.OTPRequestsPerHourEmail,
		OTPRequestsPerHourIP:    cfg.Auth.OTPRequestsPerHourIP,
		LoginAttemptsPerEmail:   cfg.Auth.LoginAttemptsPerEmail,
		LoginAttemptsPerIP:      cfg.Auth.LoginAttemptsPerIP,
	})
	if err != nil {
		return fail("rate-limit policy", err)
	}
	pacer, err := service.NewPacer(clock, cfg.Auth.ResponseFloor, cfg.Auth.ResponseJitter)
	if err != nil {
		return fail("response pacer", err)
	}
	oidc, err := service.NewOIDCCodec(cfg.Auth.OIDCSecret.Bytes())
	if err != nil {
		return fail("oidc codec", err)
	}
	// Set only when Google is configured: an interface that holds a nil *identity.Google is not nil
	// and would turn Google sign-in on.
	var google port.IdentityProvider
	if cfg.Google.Enabled() {
		g, err := identity.NewGoogle(identity.GoogleConfig{
			Issuer: identity.GoogleIssuer, ClientID: cfg.Google.ClientID, ClientSecret: cfg.Google.ClientSecret.Reveal(),
			RedirectURL: cfg.Google.RedirectURI, Timeout: googleTimeout,
		})
		if err != nil {
			return fail("google identity provider", err)
		}
		google = g
	}
	tlsMode, err := smtpTLSMode(cfg.SMTP.TLS)
	if err != nil {
		return fail("smtp tls mode", err)
	}
	smtp, err := mail.NewSMTPMailer(mail.SMTPConfig{
		Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, Username: cfg.SMTP.Username, Password: cfg.SMTP.Password.Reveal(),
		From: cfg.SMTP.From, TLS: tlsMode, Timeout: cfg.SMTP.Timeout,
	})
	if err != nil {
		return fail("smtp mailer", err)
	}
	dispatcher, err := mail.NewDispatcher(mail.DispatcherConfig{Sender: smtp, Logger: log, SendTimeout: cfg.SMTP.Timeout})
	if err != nil {
		return fail("mail dispatcher", err)
	}
	auth, err := service.NewAuth(service.AuthDeps{
		Store: store, UnitOfWork: store, Clock: clock, Hasher: hasher, OTP: otp, Limits: limits, Pacer: pacer,
		Mailer: dispatcher, Logger: log, SessionTTL: cfg.Auth.SessionTTL, OTPMaxAttempts: cfg.Auth.OTPMaxAttempts,
		Identity: google, OIDC: oidc,
	})
	if err != nil {
		// The workers are already running; stop them so a failed build leaks no goroutines.
		_ = dispatcher.Close(context.Background())
		return fail("auth service", err)
	}
	return authParts{
		options: httpadapter.AuthOptions{
			Service: auth, Verification: auth, Passwords: passwords, CSRF: csrf,
			ClientIP:     httpadapter.NewClientIPResolver(cfg.Auth.TrustedProxies),
			PublicOrigin: cfg.PublicOrigin, SecureCookies: secureCookies(cfg.PublicOrigin),
		},
		mail: dispatcher,
	}, nil
}
