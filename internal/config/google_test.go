package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func googleEnv(env map[string]string) map[string]string {
	env["GOOGLE_CLIENT_ID"] = "1234-abc.apps.googleusercontent.com"
	env["GOOGLE_CLIENT_SECRET"] = "GOCSPX-test-secret-ZQXJ"
	env["GOOGLE_REDIRECT_URI"] = env["PUBLIC_ORIGIN"] + GoogleCallbackPath
	return env
}

func TestLoadGoogleIsOffByDefault(t *testing.T) {
	cfg, err := Load(envFrom(validDevEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Google.Enabled() || cfg.Google != (GoogleConfig{}) {
		t.Errorf("Google = %+v, want off and empty", cfg.Google)
	}
}

func TestLoadGoogle(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"development": googleEnv(validDevEnv()),
		"production":  googleEnv(validProdEnv()),
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(envFrom(env))
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			g := cfg.Google
			if !g.Enabled() || g.ClientID != env["GOOGLE_CLIENT_ID"] || g.ClientSecret.Reveal() != env["GOOGLE_CLIENT_SECRET"] ||
				g.RedirectURI != env["PUBLIC_ORIGIN"]+GoogleCallbackPath {
				t.Errorf("Google = %+v", g)
			}
		})
	}
}

func TestLoadGoogleNeedsAllThreeValues(t *testing.T) {
	for _, missing := range []string{"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URI"} {
		t.Run(missing, func(t *testing.T) {
			env := googleEnv(validDevEnv())
			delete(env, missing)
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), "missing "+missing) {
				t.Fatalf("Load() error = %v, want it to name %s", err, missing)
			}
			if strings.Contains(err.Error(), "ZQXJ") || strings.Contains(err.Error(), "apps.googleusercontent.com") {
				t.Errorf("error %q echoes a configured value", err)
			}
		})
	}
	env := validDevEnv()
	env["GOOGLE_CLIENT_SECRET"] = "   "
	if _, err := Load(envFrom(env)); err != nil {
		t.Errorf("only blank Google values = %v, want Google off without an error", err)
	}
}

func TestLoadGoogleRedirectMustBeTheCallbackOfThisOrigin(t *testing.T) {
	for name, uri := range map[string]string{
		"other host":     "http://evil.example" + GoogleCallbackPath,
		"other port":     "http://localhost:8081" + GoogleCallbackPath,
		"other path":     "http://localhost:8080/callback",
		"trailing slash": "http://localhost:8080" + GoogleCallbackPath + "/",
		"with a query":   "http://localhost:8080" + GoogleCallbackPath + "?next=/",
	} {
		t.Run(name, func(t *testing.T) {
			env := googleEnv(validDevEnv())
			env["GOOGLE_REDIRECT_URI"] = uri
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), "GOOGLE_REDIRECT_URI must be PUBLIC_ORIGIN followed by "+GoogleCallbackPath) {
				t.Fatalf("Load() error = %v, want the redirect URI rule", err)
			}
			if strings.Contains(err.Error(), uri) {
				t.Errorf("error %q echoes the configured value", err)
			}
		})
	}
}

func TestConfigNeverPrintsTheGoogleSecret(t *testing.T) {
	cfg, err := Load(envFrom(googleEnv(validDevEnv())))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("config", slog.Any("config", cfg), slog.Any("google", cfg.Google))
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"%v": fmt.Sprintf("%v", cfg), "%+v": fmt.Sprintf("%+v", cfg), "%#v": fmt.Sprintf("%#v", cfg),
		"google %+v": fmt.Sprintf("%+v", cfg.Google), "json": string(data), "slog": logs.String(),
	} {
		if strings.Contains(out, "ZQXJ") {
			t.Errorf("%s leaks the Google client secret: %s", name, out)
		}
	}
	if !strings.Contains(cfg.String(), "google=true") || !strings.Contains(logs.String(), `"google_enabled":true`) {
		t.Errorf("String() = %s, log = %s; want google=true and google_enabled", cfg.String(), logs.String())
	}
}
