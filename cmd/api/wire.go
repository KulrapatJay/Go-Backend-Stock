package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"go-backend-stock/internal/adapter/alpaca"
	"go-backend-stock/internal/adapter/demo"
	httpadapter "go-backend-stock/internal/adapter/http"
	"go-backend-stock/internal/adapter/identity"
	"go-backend-stock/internal/adapter/mail"
	"go-backend-stock/internal/adapter/postgres"
	"go-backend-stock/internal/config"
	"go-backend-stock/internal/core/domain"
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
	limits, err := buildRateLimits(cfg)
	if err != nil {
		return fail("rate limits", err)
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
			Service: auth, Verification: auth, PasswordReset: auth.PasswordReset(), Passwords: passwords, CSRF: csrf,
			ClientIP:     httpadapter.NewClientIPResolver(cfg.Auth.TrustedProxies),
			PublicOrigin: cfg.PublicOrigin, SecureCookies: secureCookies(cfg.PublicOrigin),
		},
		mail: dispatcher,
	}, nil
}

// buildRateLimits builds the rate-limit policy that auth and portfolios share.
func buildRateLimits(cfg config.Config) (*service.RateLimitPolicy, error) {
	keys, err := service.NewRateLimitKeys(cfg.Auth.RateLimitSecret.Bytes())
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	policy, err := service.NewRateLimitPolicy(keys, service.RateLimitConfig{
		OTPResendCooldown:       cfg.Auth.OTPResendCooldown,
		OTPRequestsPerHourEmail: cfg.Auth.OTPRequestsPerHourEmail,
		OTPRequestsPerHourIP:    cfg.Auth.OTPRequestsPerHourIP,
		LoginAttemptsPerEmail:   cfg.Auth.LoginAttemptsPerEmail,
		LoginAttemptsPerIP:      cfg.Auth.LoginAttemptsPerIP,
	})
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	return policy, nil
}

// buildPortfolios wires the portfolio use cases over the same store as auth. It opens no connection.
func buildPortfolios(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (httpadapter.PortfolioOptions, error) {
	limits, err := buildRateLimits(cfg)
	if err != nil {
		return httpadapter.PortfolioOptions{}, fmt.Errorf("build portfolios: rate limits: %w", err)
	}
	store := postgres.NewStore(pool)
	portfolios, err := service.NewPortfolios(service.PortfolioDeps{
		Store: store, UnitOfWork: store, Clock: systemClock{}, Limits: limits, Logger: log,
	})
	if err != nil {
		return httpadapter.PortfolioOptions{}, fmt.Errorf("build portfolios: %w", err)
	}
	return httpadapter.PortfolioOptions{Service: portfolios}, nil
}

// instrumentProvider picks the source that checks new symbols from PRICE_PROVIDER. Production never
// gets the demo list, and an unavailable real provider is never replaced by sample data. The Alpaca
// secret is revealed only here, to build the adapter.
func instrumentProvider(cfg config.Config) (port.InstrumentProvider, error) {
	switch {
	case cfg.PriceProvider == config.ProviderDemo && !cfg.IsProduction():
		return demo.NewInstruments(), nil
	case cfg.PriceProvider == config.ProviderAlpaca:
		assets, err := alpaca.NewAssets(alpaca.AssetsConfig{
			BaseURL: cfg.Alpaca.TradingBaseURL, KeyID: cfg.Alpaca.Key.Reveal(), Secret: cfg.Alpaca.Secret.Reveal(),
			Timeout: alpaca.DefaultTimeout, NegativeTTL: alpaca.DefaultNegativeTTL, NegativeMax: alpaca.DefaultNegativeMax,
		})
		if err != nil {
			return nil, fmt.Errorf("alpaca instrument provider: %w", err)
		}
		return assets, nil
	case cfg.PriceProvider == config.ProviderDemo:
		return nil, errors.New("the demo instrument provider cannot be used in production")
	default:
		return nil, errors.New("unknown PRICE_PROVIDER")
	}
}

// priceProvider picks the source of prices from PRICE_PROVIDER, like instrumentProvider. When Alpaca
// cannot answer, a portfolio is valued without prices; it is never given the demo prices instead.
func priceProvider(cfg config.Config, clock port.Clock) (port.PriceProvider, error) {
	switch {
	case cfg.PriceProvider == config.ProviderDemo && !cfg.IsProduction():
		return demo.NewPrices(clock), nil
	case cfg.PriceProvider == config.ProviderAlpaca:
		prices, err := alpaca.NewPrices(alpaca.PricesConfig{
			DataBaseURL: cfg.Alpaca.DataBaseURL, TradingBaseURL: cfg.Alpaca.TradingBaseURL,
			KeyID: cfg.Alpaca.Key.Reveal(), Secret: cfg.Alpaca.Secret.Reveal(),
			Timeout: alpaca.DefaultTimeout, Now: clock.Now,
		})
		if err != nil {
			return nil, fmt.Errorf("alpaca price provider: %w", err)
		}
		return prices, nil
	case cfg.PriceProvider == config.ProviderDemo:
		return nil, errors.New("the demo price provider cannot be used in production")
	default:
		return nil, errors.New("unknown PRICE_PROVIDER")
	}
}

// buildTransactions wires the trade use cases over the same store as auth and portfolios. It opens
// no connection. The clock is also the HTTP adapter's, which checks that executed_at is not in the
// future.
func buildTransactions(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (httpadapter.TransactionOptions, error) {
	limits, err := buildRateLimits(cfg)
	if err != nil {
		return httpadapter.TransactionOptions{}, fmt.Errorf("build transactions: rate limits: %w", err)
	}
	instruments, err := instrumentProvider(cfg)
	if err != nil {
		return httpadapter.TransactionOptions{}, fmt.Errorf("build transactions: %w", err)
	}
	store := postgres.NewStore(pool)
	clock := systemClock{}
	transactions, err := service.NewTransactions(service.TransactionDeps{
		Store: store, UnitOfWork: store, Clock: clock, Limits: limits, Instruments: instruments, Logger: log,
	})
	if err != nil {
		return httpadapter.TransactionOptions{}, fmt.Errorf("build transactions: %w", err)
	}
	return httpadapter.TransactionOptions{Service: transactions, Clock: clock}, nil
}

// buildValuation wires the holdings and valuation use cases over the same store as the others. It
// opens no connection.
func buildValuation(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (httpadapter.ValuationOptions, error) {
	prices, err := priceProvider(cfg, systemClock{})
	if err != nil {
		return httpadapter.ValuationOptions{}, fmt.Errorf("build valuation: %w", err)
	}
	valuation, err := service.NewValuation(service.ValuationDeps{
		Store: postgres.NewStore(pool), Prices: prices, Clock: systemClock{}, Logger: log,
	})
	if err != nil {
		return httpadapter.ValuationOptions{}, fmt.Errorf("build valuation: %w", err)
	}
	return httpadapter.ValuationOptions{Service: valuation}, nil
}

// barSource is the source label of the stored daily bars for the configured provider. The stored bars of one
// source are never read as another's, so switching PRICE_PROVIDER never serves sample bars as real prices.
func barSource(cfg config.Config) (domain.BarSource, error) {
	switch cfg.PriceProvider {
	case config.ProviderDemo:
		return domain.BarSourceDemo, nil
	case config.ProviderAlpaca:
		return domain.BarSourceAlpaca, nil
	}
	return "", errors.New("unknown PRICE_PROVIDER")
}

// buildPriceHistory wires the daily bars use case over the same store, providers and rate limits as the
// others. It opens no connection.
func buildPriceHistory(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (httpadapter.PriceHistoryOptions, error) {
	fail := func(what string, err error) (httpadapter.PriceHistoryOptions, error) {
		return httpadapter.PriceHistoryOptions{}, fmt.Errorf("build price history: %s: %w", what, err)
	}
	clock := systemClock{}
	source, err := barSource(cfg)
	if err != nil {
		return fail("source", err)
	}
	prices, err := priceProvider(cfg, clock)
	if err != nil {
		return fail("prices", err)
	}
	instruments, err := instrumentProvider(cfg)
	if err != nil {
		return fail("instruments", err)
	}
	limits, err := buildRateLimits(cfg)
	if err != nil {
		return fail("rate limits", err)
	}
	store := postgres.NewStore(pool)
	history, err := service.NewPriceHistory(service.PriceHistoryDeps{
		Store: store, UnitOfWork: store, Prices: prices, Instruments: instruments, Source: source,
		Clock: clock, Limits: limits, Logger: log,
	})
	if err != nil {
		return fail("service", err)
	}
	return httpadapter.PriceHistoryOptions{Service: history}, nil
}
