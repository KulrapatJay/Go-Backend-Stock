// Package config loads and validates process configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Environment selects the validation rules applied to the configuration.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvTest        Environment = "test"
	EnvProduction  Environment = "production"
)

// Config is the validated configuration of the API process.
type Config struct {
	Env                 Environment
	HTTPAddr            string
	PublicOrigin        string
	DatabaseURL         string
	DatabaseMaxConns    int
	LogLevel            slog.Level
	DocsEnabled         bool
	DocsTryItOutEnabled bool
	ShutdownTimeout     time.Duration
	Auth                AuthConfig
	SMTP                SMTPConfig
}

// IsProduction reports whether production-only rules apply.
func (c Config) IsProduction() bool { return c.Env == EnvProduction }

// Load reads every setting through getenv and returns all validation errors at once.
// Error messages never include configured values because they may contain secrets.
func Load(getenv func(string) string) (Config, error) {
	var cfg Config
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	switch env := Environment(strings.TrimSpace(getenv("APP_ENV"))); env {
	case EnvDevelopment, EnvTest, EnvProduction:
		cfg.Env = env
	default:
		add(errors.New("APP_ENV must be one of development, test, production"))
	}
	prod := cfg.IsProduction()

	cfg.HTTPAddr = valueOr(getenv("HTTP_ADDR"), ":8080")

	var err error
	cfg.PublicOrigin, err = parseOrigin(getenv("PUBLIC_ORIGIN"), prod)
	add(err)
	cfg.DatabaseURL, err = parseDatabaseURL(getenv("DATABASE_URL"), prod)
	add(err)
	cfg.DatabaseMaxConns, err = parseIntInRange("DB_MAX_CONNS", getenv("DB_MAX_CONNS"), 5, 1, 50)
	add(err)
	cfg.LogLevel, err = parseLogLevel(getenv("LOG_LEVEL"), prod)
	add(err)
	cfg.DocsEnabled, err = parseBool("DOCS_ENABLED", getenv("DOCS_ENABLED"), !prod)
	add(err)
	cfg.DocsTryItOutEnabled, err = parseBool("DOCS_TRY_IT_OUT_ENABLED", getenv("DOCS_TRY_IT_OUT_ENABLED"), !prod)
	add(err)
	if cfg.DocsTryItOutEnabled && !cfg.DocsEnabled {
		add(errors.New("DOCS_TRY_IT_OUT_ENABLED requires DOCS_ENABLED=true"))
	}
	cfg.ShutdownTimeout, err = parseDurationInRange("SHUTDOWN_TIMEOUT", getenv("SHUTDOWN_TIMEOUT"), 10*time.Second, time.Second, time.Minute)
	add(err)

	var authErrs, smtpErrs []error
	cfg.Auth, authErrs = loadAuth(getenv, prod)
	cfg.SMTP, smtpErrs = loadSMTP(getenv, prod)
	errs = append(errs, authErrs...)
	errs = append(errs, smtpErrs...)

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func valueOr(v, fallback string) string {
	if v = strings.TrimSpace(v); v == "" {
		return fallback
	}
	return v
}

func parseOrigin(raw string, prod bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("PUBLIC_ORIGIN is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("PUBLIC_ORIGIN must be an origin such as https://example.com")
	}
	if prod && u.Scheme != "https" {
		return "", errors.New("PUBLIC_ORIGIN must use https in production")
	}
	return u.Scheme + "://" + u.Host, nil
}

func parseDatabaseURL(raw string, prod bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("DATABASE_URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		// Never wrap err or echo raw: both can contain the password.
		return "", errors.New("DATABASE_URL must be a postgres:// URL")
	}
	if prod && u.Query().Get("sslmode") != "verify-full" {
		return "", errors.New("DATABASE_URL must set sslmode=verify-full in production")
	}
	return raw, nil
}

func parseIntInRange(name, raw string, fallback, minValue, maxValue int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < minValue || v > maxValue {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minValue, maxValue)
	}
	return v, nil
}

func parseLogLevel(raw string, prod bool) (slog.Level, error) {
	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		level = slog.LevelInfo
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return 0, errors.New("LOG_LEVEL must be one of debug, info, warn, error")
	}
	if prod && level < slog.LevelInfo {
		return 0, errors.New("LOG_LEVEL=debug is not allowed in production")
	}
	return level, nil
}

func parseBool(name, raw string, fallback bool) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return v, nil
}

func parseDurationInRange(name, raw string, fallback, minValue, maxValue time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v < minValue || v > maxValue {
		return 0, fmt.Errorf("%s must be a duration between %s and %s", name, minValue, maxValue)
	}
	return v, nil
}
