package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"go-backend-stock/internal/adapter/demo"
	httpadapter "go-backend-stock/internal/adapter/http"
	"go-backend-stock/internal/config"
	"go-backend-stock/internal/core/domain"
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
	if o.Service == nil || o.Verification == nil || o.Passwords == nil || o.PasswordReset == nil || o.CSRF == nil || o.ClientIP == nil ||
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

func TestBuildServesThePortfolioRoutes(t *testing.T) {
	cfg := wireConfig(t, "http://localhost:8080")
	pool, err := pgxpool.New(t.Context(), cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	auth, err := buildAuth(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildAuth() error = %v", err)
	}
	defer func() { _ = auth.mail.Close(context.Background()) }()
	portfolios, err := buildPortfolios(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildPortfolios() error = %v", err)
	}
	if portfolios.Service == nil {
		t.Fatal("buildPortfolios() left the service unset")
	}

	app := httpadapter.NewServer(httpadapter.Options{Logger: log, Readiness: pool, Auth: auth.options, Portfolios: portfolios})
	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/v1/portfolios", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("GET /api/v1/portfolios without a session = %d, want 401 (404 means the route is not wired)", resp.StatusCode)
	}
}

func TestBuildServesTheTransactionRoutes(t *testing.T) {
	cfg := wireConfig(t, "http://localhost:8080")
	pool, err := pgxpool.New(t.Context(), cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	auth, err := buildAuth(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildAuth() error = %v", err)
	}
	defer func() { _ = auth.mail.Close(context.Background()) }()
	portfolios, err := buildPortfolios(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildPortfolios() error = %v", err)
	}
	transactions, err := buildTransactions(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildTransactions() error = %v", err)
	}
	if transactions.Service == nil || transactions.Clock == nil {
		t.Fatalf("buildTransactions() = %+v, want the service and the clock set", transactions)
	}

	app := httpadapter.NewServer(httpadapter.Options{
		Logger: log, Readiness: pool, Auth: auth.options, Portfolios: portfolios, Transactions: transactions,
	})
	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet,
		"/api/v1/portfolios/00000000-0000-4000-8000-000000000000/transactions", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("GET /api/v1/portfolios/{id}/transactions without a session = %d, want 401 (404 means the route is not wired)", resp.StatusCode)
	}
}

// PRICE_PROVIDER decides where a symbol the database does not know yet is checked.
func TestInstrumentProviderFollowsPriceProvider(t *testing.T) {
	aapl, err := domain.ParseSymbol("AAPL")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("demo", func(t *testing.T) {
		provider, err := instrumentProvider(config.Config{Env: config.EnvDevelopment, PriceProvider: config.ProviderDemo})
		if err != nil {
			t.Fatalf("instrumentProvider() error = %v", err)
		}
		got, err := provider.Lookup(t.Context(), aapl)
		if err != nil || got.Symbol.String() != "AAPL" || got.Source != domain.InstrumentSourceDemo {
			t.Errorf("Lookup(AAPL) = %+v, %v, want the demo instrument", got, err)
		}
	})

	t.Run("alpaca", func(t *testing.T) {
		var gotKey, gotPath string
		fake := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			gotKey, gotPath = r.Header.Get("APCA-API-KEY-ID"), r.URL.Path
			_, _ = w.Write([]byte(`{"id":"b0b6dd9d-8b9b-48a9-ba46-b9d54906e415","class":"us_equity","exchange":"NASDAQ","symbol":"AAPL","name":"Apple Inc.","status":"active"}`))
		}))
		defer fake.Close()
		cfg := config.Config{Env: config.EnvDevelopment, PriceProvider: config.ProviderAlpaca, Alpaca: config.AlpacaConfig{
			Key: "key-id", Secret: "key-secret", TradingBaseURL: fake.URL}}
		provider, err := instrumentProvider(cfg)
		if err != nil {
			t.Fatalf("instrumentProvider() error = %v", err)
		}
		got, err := provider.Lookup(t.Context(), aapl)
		if err != nil || got.Source != domain.InstrumentSourceAlpaca || got.ProviderAssetID == "" {
			t.Fatalf("Lookup(AAPL) = %+v, %v, want an Alpaca instrument", got, err)
		}
		if gotKey != "key-id" || gotPath != "/v2/assets/AAPL" {
			t.Errorf("Alpaca saw key %q and path %q", gotKey, gotPath)
		}
	})

	t.Run("alpaca without credentials", func(t *testing.T) {
		_, err := instrumentProvider(config.Config{Env: config.EnvDevelopment, PriceProvider: config.ProviderAlpaca})
		if err == nil {
			t.Fatal("instrumentProvider() error = nil, want an error for missing credentials")
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		if _, err := instrumentProvider(config.Config{Env: config.EnvDevelopment, PriceProvider: "iex"}); err == nil {
			t.Fatal("instrumentProvider() error = nil, want an error")
		}
	})
}

// Demo instruments are sample data, so a production API must never answer from them: a production
// configuration that passes config.Load has to select Alpaca.
func TestProductionConfigCannotReachTheDemoInstruments(t *testing.T) {
	env := map[string]string{
		"APP_ENV": "production", "PUBLIC_ORIGIN": "https://stock.example.com",
		"DATABASE_URL":      "postgres://app_runtime:prodpass@db.example.com:5432/postgres?sslmode=verify-full",
		"CSRF_SECRET":       strings.Repeat("c", 32),
		"OTP_SECRET":        strings.Repeat("o", 32),
		"RATE_LIMIT_SECRET": strings.Repeat("r", 32),
		"OIDC_SECRET":       strings.Repeat("i", 32),
		"SMTP_HOST":         "smtp.example.com", "SMTP_PORT": "587", "SMTP_FROM": "no-reply@example.com", "SMTP_TLS": "starttls",
		"PRICE_PROVIDER": "alpaca", "ALPACA_KEY": "key-id", "ALPACA_SECRET": "key-secret",
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	provider, err := instrumentProvider(cfg)
	if err != nil {
		t.Fatalf("instrumentProvider() error = %v", err)
	}
	if _, isDemo := provider.(*demo.Instruments); isDemo {
		t.Fatal("a production configuration selected the demo instruments")
	}

	// Even a hand-built production config that asks for demo must not get it.
	if _, err := instrumentProvider(config.Config{Env: config.EnvProduction, PriceProvider: config.ProviderDemo}); err == nil {
		t.Error("instrumentProvider(production, demo) error = nil, want a refusal")
	}
}

// PRICE_PROVIDER also decides where prices come from. Until the Alpaca price adapter exists, alpaca
// means "no prices", never the demo prices.
func TestPriceProviderFollowsPriceProvider(t *testing.T) {
	aapl, err := domain.ParseSymbol("AAPL")
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 11, 4, 14, 30, 0, 0, time.UTC)

	t.Run("demo", func(t *testing.T) {
		provider, err := priceProvider(config.Config{Env: config.EnvDevelopment, PriceProvider: config.ProviderDemo}, systemClock{})
		if err != nil {
			t.Fatalf("priceProvider() error = %v", err)
		}
		batch, err := provider.Quotes(t.Context(), []domain.Symbol{aapl}, cutoff)
		if q := batch.Quotes["AAPL"]; err != nil || !q.IsDemo || q.Price.String() != "200" {
			t.Errorf("Quotes(AAPL) = %+v, %v, want the demo price", batch, err)
		}
	})

	t.Run("alpaca has no prices yet", func(t *testing.T) {
		provider, err := priceProvider(config.Config{Env: config.EnvDevelopment, PriceProvider: config.ProviderAlpaca}, systemClock{})
		if err != nil {
			t.Fatalf("priceProvider() error = %v", err)
		}
		batch, err := provider.Quotes(t.Context(), []domain.Symbol{aapl}, cutoff)
		if !errors.Is(err, domain.ErrDependencyUnavailable) || len(batch.Quotes) != 0 {
			t.Errorf("Quotes(AAPL) = %+v, %v; want dependency_unavailable and no quote, never a demo price", batch, err)
		}
	})

	t.Run("production never gets demo prices", func(t *testing.T) {
		if _, err := priceProvider(config.Config{Env: config.EnvProduction, PriceProvider: config.ProviderDemo}, systemClock{}); err == nil {
			t.Error("priceProvider(production, demo) error = nil, want a refusal")
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		if _, err := priceProvider(config.Config{Env: config.EnvDevelopment, PriceProvider: "iex"}, systemClock{}); err == nil {
			t.Error("priceProvider(iex) error = nil, want an error")
		}
	})
}

func TestBuildServesTheValuationRoutes(t *testing.T) {
	cfg := wireConfig(t, "http://localhost:8080")
	pool, err := pgxpool.New(t.Context(), cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	auth, err := buildAuth(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildAuth() error = %v", err)
	}
	defer func() { _ = auth.mail.Close(context.Background()) }()
	valuation, err := buildValuation(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildValuation() error = %v", err)
	}
	if valuation.Service == nil {
		t.Fatal("buildValuation() left the service unset")
	}

	app := httpadapter.NewServer(httpadapter.Options{Logger: log, Readiness: pool, Auth: auth.options, Valuation: valuation})
	for _, path := range []string{"holdings", "valuation"} {
		resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet,
			"/api/v1/portfolios/00000000-0000-4000-8000-000000000000/"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != nethttp.StatusUnauthorized {
			t.Errorf("GET /api/v1/portfolios/{id}/%s without a session = %d, want 401 (404 means the route is not wired)", path, resp.StatusCode)
		}
	}
}

// With PRICE_PROVIDER=alpaca and no Alpaca price adapter yet, the API still starts and values a
// portfolio without prices; it must not start with another provider's prices.
func TestBuildValuationRejectsAProductionDemoProvider(t *testing.T) {
	cfg := config.Config{Env: config.EnvProduction, PriceProvider: config.ProviderDemo}
	if _, err := buildValuation(cfg, nil, slog.New(slog.NewJSONHandler(io.Discard, nil))); err == nil {
		t.Fatal("buildValuation(production, demo) error = nil, want a refusal")
	}
}
