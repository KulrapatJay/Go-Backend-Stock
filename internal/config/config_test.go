package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func validDevEnv() map[string]string {
	return map[string]string{
		"APP_ENV":       "development",
		"PUBLIC_ORIGIN": "http://localhost:8080",
		"DATABASE_URL":  "postgres://app_runtime:devpass@postgres:5432/stock?sslmode=disable",
	}
}

func TestLoadDevelopmentDefaults(t *testing.T) {
	cfg, err := Load(envFrom(validDevEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Env != EnvDevelopment {
		t.Errorf("Env = %q, want %q", cfg.Env, EnvDevelopment)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.PublicOrigin != "http://localhost:8080" {
		t.Errorf("PublicOrigin = %q", cfg.PublicOrigin)
	}
	if cfg.DatabaseMaxConns != 5 {
		t.Errorf("DatabaseMaxConns = %d, want 5", cfg.DatabaseMaxConns)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want INFO", cfg.LogLevel)
	}
	if !cfg.DocsEnabled || !cfg.DocsTryItOutEnabled {
		t.Errorf("docs flags = %v/%v, want true/true in development", cfg.DocsEnabled, cfg.DocsTryItOutEnabled)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
}

func TestLoadProductionDefaultsDisableDocs(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{
		"APP_ENV":       "production",
		"PUBLIC_ORIGIN": "https://stock.example.com",
		"DATABASE_URL":  "postgres://app_runtime:prodpass@db.example.com:5432/postgres?sslmode=verify-full",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DocsEnabled || cfg.DocsTryItOutEnabled {
		t.Errorf("docs flags = %v/%v, want false/false by default in production", cfg.DocsEnabled, cfg.DocsTryItOutEnabled)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	production := func(e map[string]string) {
		e["APP_ENV"] = "production"
		e["PUBLIC_ORIGIN"] = "https://stock.example.com"
		e["DATABASE_URL"] = "postgres://u:p@db.example.com:5432/postgres?sslmode=verify-full"
	}
	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{"missing app env", func(e map[string]string) { delete(e, "APP_ENV") }, "APP_ENV"},
		{"unknown app env", func(e map[string]string) { e["APP_ENV"] = "staging" }, "APP_ENV"},
		{"missing origin", func(e map[string]string) { delete(e, "PUBLIC_ORIGIN") }, "PUBLIC_ORIGIN is required"},
		{"origin with path", func(e map[string]string) { e["PUBLIC_ORIGIN"] = "http://localhost:8080/app" }, "PUBLIC_ORIGIN must be an origin"},
		{"production http origin", func(e map[string]string) {
			production(e)
			e["PUBLIC_ORIGIN"] = "http://stock.example.com"
		}, "must use https in production"},
		{"production debug log", func(e map[string]string) {
			production(e)
			e["LOG_LEVEL"] = "debug"
		}, "LOG_LEVEL=debug"},
		{"production without verify-full", func(e map[string]string) {
			production(e)
			e["DATABASE_URL"] = "postgres://u:p@db.example.com:5432/postgres?sslmode=require"
		}, "sslmode=verify-full"},
		{"non postgres url", func(e map[string]string) { e["DATABASE_URL"] = "mysql://u:p@db:3306/stock" }, "postgres:// URL"},
		{"unknown log level", func(e map[string]string) { e["LOG_LEVEL"] = "verbose" }, "LOG_LEVEL must be one of"},
		{"max conns zero", func(e map[string]string) { e["DB_MAX_CONNS"] = "0" }, "DB_MAX_CONNS"},
		{"max conns text", func(e map[string]string) { e["DB_MAX_CONNS"] = "abc" }, "DB_MAX_CONNS"},
		{"bad bool", func(e map[string]string) { e["DOCS_ENABLED"] = "yes please" }, "DOCS_ENABLED must be true or false"},
		{"try it out without docs", func(e map[string]string) {
			e["DOCS_ENABLED"] = "false"
			e["DOCS_TRY_IT_OUT_ENABLED"] = "true"
		}, "requires DOCS_ENABLED=true"},
		{"shutdown too long", func(e map[string]string) { e["SHUTDOWN_TIMEOUT"] = "5m" }, "SHUTDOWN_TIMEOUT"},
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

func TestLoadErrorsNeverEchoDatabasePassword(t *testing.T) {
	_, err := Load(envFrom(map[string]string{
		"APP_ENV":       "production",
		"PUBLIC_ORIGIN": "https://stock.example.com",
		"DATABASE_URL":  "postgres://app_runtime:S3cretPassw0rd@db.example.com:5432/postgres?sslmode=disable",
	}))
	if err == nil {
		t.Fatal("Load() error = nil, want sslmode error")
	}
	if strings.Contains(err.Error(), "S3cretPassw0rd") {
		t.Fatalf("error leaked the database password: %v", err)
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := Load(envFrom(map[string]string{}))
	if err == nil {
		t.Fatal("Load() error = nil, want errors")
	}
	for _, want := range []string{"APP_ENV", "PUBLIC_ORIGIN", "DATABASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}
