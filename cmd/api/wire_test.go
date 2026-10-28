package main

import (
	"context"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	httpadapter "go-backend-stock/internal/adapter/http"
	"go-backend-stock/internal/config"
)

func wireConfig(t *testing.T, origin string, extra ...map[string]string) config.Config {
	t.Helper()
	env := map[string]string{
		"APP_ENV":           "test",
		"PUBLIC_ORIGIN":     origin,
		"DATABASE_URL":      "postgres://app_runtime:unused@127.0.0.1:1/stock?sslmode=disable",
		"CSRF_SECRET":       "dev-only-csrf-secret-0123456789abcdef",
		"OTP_SECRET":        "dev-only-otp-secret-0123456789abcdef",
		"RATE_LIMIT_SECRET": "dev-only-rate-limit-secret-0123456789",
		"OIDC_SECRET":       "dev-only-oidc-secret-0123456789abcdef",
	}
	for _, more := range extra {
		for k, v := range more {
			env[k] = v
		}
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	return cfg
}

// The pool connects lazily, so the wiring can be checked without a database: GET /auth/csrf
// without a session cookie never queries it.
func TestBuildAuthServesTheAuthRoutes(t *testing.T) {
	cfg := wireConfig(t, "http://localhost:8080")
	pool, err := pgxpool.New(t.Context(), cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	parts, err := buildAuth(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildAuth() error = %v", err)
	}
	defer func() { _ = parts.mail.Close(context.Background()) }()
	o := parts.options
	if o.Service == nil || o.Verification == nil || o.Passwords == nil || o.CSRF == nil || o.ClientIP == nil ||
		o.PublicOrigin != "http://localhost:8080" || o.SecureCookies {
		t.Fatalf("options = %+v", o)
	}

	app := httpadapter.NewServer(httpadapter.Options{Logger: log, Readiness: pool, Auth: o})
	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/v1/auth/csrf", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK || !strings.Contains(strings.Join(resp.Header.Values("Set-Cookie"), ";"), "csrf_nonce=") {
		t.Errorf("GET /api/v1/auth/csrf = %d %v, want 200 with a csrf_nonce cookie", resp.StatusCode, resp.Header.Values("Set-Cookie"))
	}
}

func TestSecureCookiesFollowThePublicOrigin(t *testing.T) {
	if secureCookies("http://localhost:8080") || !secureCookies("https://stock.example.com") {
		t.Error("only an https origin may use Secure, __Host- cookies")
	}
}

func TestSystemClockIsUTC(t *testing.T) {
	if (systemClock{}).Now().Location() != time.UTC {
		t.Error("systemClock must return UTC")
	}
}

func TestSMTPTLSModeMapsEveryConfigValue(t *testing.T) {
	for _, v := range []string{config.SMTPTLSNone, config.SMTPTLSStartTLS, config.SMTPTLSImplicit} {
		got, err := smtpTLSMode(v)
		if err != nil || string(got) != v {
			t.Errorf("smtpTLSMode(%q) = %q, %v, want the same value", v, got, err)
		}
	}
	if _, err := smtpTLSMode("ssl"); err == nil {
		t.Error("smtpTLSMode(\"ssl\") accepted an unknown value")
	}
}

func authConfigOf(t *testing.T, cfg config.Config) string {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	parts, err := buildAuth(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildAuth() error = %v", err)
	}
	defer func() { _ = parts.mail.Close(context.Background()) }()
	app := httpadapter.NewServer(httpadapter.Options{Logger: log, Readiness: pool, Auth: parts.options})
	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/v1/auth/config", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestBuildAuthLeavesGoogleOffByDefault(t *testing.T) {
	if got := authConfigOf(t, wireConfig(t, "http://localhost:8080")); got != `{"google_enabled":false}` {
		t.Errorf("GET /api/v1/auth/config = %s", got)
	}
}

// Building the Google adapter makes no network call (discovery is lazy), so this needs no Google.
func TestBuildAuthTurnsGoogleOnWithItsConfig(t *testing.T) {
	cfg := wireConfig(t, "http://localhost:8080", map[string]string{
		"GOOGLE_CLIENT_ID":     "1234-abc.apps.googleusercontent.com",
		"GOOGLE_CLIENT_SECRET": "GOCSPX-test",
		"GOOGLE_REDIRECT_URI":  "http://localhost:8080/api/v1/auth/google/callback",
	})
	if got := authConfigOf(t, cfg); got != `{"google_enabled":true}` {
		t.Errorf("GET /api/v1/auth/config = %s", got)
	}
}
