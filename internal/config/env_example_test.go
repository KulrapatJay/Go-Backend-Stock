package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envExample reads .env.example the way docker compose does for the api service: KEY=VALUE lines,
// comments and blank lines skipped, plus the DATABASE_URL that docker-compose.yml builds from it.
func envExample(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", ".env.example"))
	if err != nil {
		t.Fatalf("open .env.example: %v", err)
	}
	defer func() { _ = f.Close() }()

	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf(".env.example line %q is not KEY=VALUE", line)
		}
		env[key] = value
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	env["DATABASE_URL"] = "postgres://app_runtime:" + env["APP_RUNTIME_PASSWORD"] + "@postgres:5432/" + env["POSTGRES_DB"] + "?sslmode=disable"
	return env
}

func TestEnvExampleStartsTheAPIInDevelopment(t *testing.T) {
	cfg, err := Load(envFrom(envExample(t)))
	if err != nil {
		t.Fatalf("Load(.env.example) error = %v; a fresh clone must start with `cp .env.example .env && docker compose up --build`", err)
	}
	if cfg.Env != EnvDevelopment {
		t.Errorf("Env = %q, want %q", cfg.Env, EnvDevelopment)
	}
	if cfg.PriceProvider != ProviderDemo {
		t.Errorf("PriceProvider = %q, want %q", cfg.PriceProvider, ProviderDemo)
	}
	if !cfg.DocsEnabled || !cfg.DocsTryItOutEnabled {
		t.Errorf("DocsEnabled = %v, DocsTryItOutEnabled = %v, want both true", cfg.DocsEnabled, cfg.DocsTryItOutEnabled)
	}
	if cfg.Google.Enabled() {
		t.Error("Google sign-in must stay off until the user sets GOOGLE_*")
	}
}

func TestEnvExampleSecretsAreRejectedInProduction(t *testing.T) {
	env := envExample(t)
	env["APP_ENV"] = "production"
	_, err := Load(envFrom(env))
	if err == nil {
		t.Fatal("Load() accepted the .env.example secrets in production")
	}
	for _, name := range []string{"CSRF_SECRET", "OTP_SECRET", "RATE_LIMIT_SECRET", "OIDC_SECRET"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
	for key, value := range env {
		if strings.HasSuffix(key, "_SECRET") && value != "" && strings.Contains(err.Error(), value) {
			t.Errorf("error leaks the value of %s", key)
		}
	}
}
