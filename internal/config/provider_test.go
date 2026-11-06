package config

import (
	"fmt"
	"strings"
	"testing"
)

const (
	testAlpacaKey    = "PKTESTKEYID0123456789"
	testAlpacaSecret = "alpaca-test-secret-ZQXJ-0123456789"
)

func alpacaEnv(env map[string]string) map[string]string {
	env["PRICE_PROVIDER"] = "alpaca"
	env["ALPACA_KEY"] = testAlpacaKey
	env["ALPACA_SECRET"] = testAlpacaSecret
	return env
}

func TestPriceProviderDefaultsToDemoOutsideProduction(t *testing.T) {
	for name, env := range map[string]map[string]string{"development": validDevEnv(), "test": func() map[string]string {
		e := validDevEnv()
		e["APP_ENV"] = "test"
		return e
	}()} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(envFrom(env))
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.PriceProvider != ProviderDemo || cfg.Alpaca != (AlpacaConfig{}) {
				t.Errorf("PriceProvider = %q, Alpaca = %+v, want demo and an empty Alpaca config", cfg.PriceProvider, cfg.Alpaca)
			}
		})
	}
}

func TestAlpacaCanBeChosenInDevelopment(t *testing.T) {
	cfg, err := Load(envFrom(alpacaEnv(validDevEnv())))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	a := cfg.Alpaca
	if cfg.PriceProvider != ProviderAlpaca || a.Key.Reveal() != testAlpacaKey || a.Secret.Reveal() != testAlpacaSecret ||
		a.TradingBaseURL != "https://paper-api.alpaca.markets" {
		t.Errorf("PriceProvider = %q, Alpaca = %+v", cfg.PriceProvider, a)
	}
}

func TestAlpacaNeedsItsKeyAndSecret(t *testing.T) {
	for _, missing := range []string{"ALPACA_KEY", "ALPACA_SECRET"} {
		t.Run(missing, func(t *testing.T) {
			env := alpacaEnv(validDevEnv())
			delete(env, missing)
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("Load() error = %v, want it to name %s", err, missing)
			}
			if strings.Contains(err.Error(), testAlpacaKey) || strings.Contains(err.Error(), testAlpacaSecret) {
				t.Errorf("error %q echoes a configured value", err)
			}
		})
	}
}

func TestProductionNeedsAlpaca(t *testing.T) {
	tests := map[string]struct {
		mutate  func(map[string]string)
		wantErr string
	}{
		"nothing set":        {func(e map[string]string) {}, "PRICE_PROVIDER"},
		"demo":               {func(e map[string]string) { e["PRICE_PROVIDER"] = "demo" }, "PRICE_PROVIDER"},
		"alpaca without key": {func(e map[string]string) { e["PRICE_PROVIDER"] = "alpaca" }, "ALPACA_KEY"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env := validProdEnv()
			delete(env, "PRICE_PROVIDER")
			delete(env, "ALPACA_KEY")
			delete(env, "ALPACA_SECRET")
			tt.mutate(env)
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %v, want it to name %s", err, tt.wantErr)
			}
		})
	}
	cfg, err := Load(envFrom(validProdEnv()))
	if err != nil {
		t.Fatalf("Load(production with Alpaca) error = %v", err)
	}
	if cfg.PriceProvider != ProviderAlpaca || cfg.Alpaca.TradingBaseURL != "https://paper-api.alpaca.markets" {
		t.Errorf("PriceProvider = %q, Alpaca = %+v", cfg.PriceProvider, cfg.Alpaca)
	}
}

func TestPriceProviderRejectsUnknownValues(t *testing.T) {
	for _, bad := range []string{"Alpaca", "ALPACA", "iex", "polygon", "demo,alpaca"} {
		t.Run(bad, func(t *testing.T) {
			env := validDevEnv()
			env["PRICE_PROVIDER"] = bad
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), "PRICE_PROVIDER") {
				t.Fatalf("Load() error = %v, want PRICE_PROVIDER error", err)
			}
		})
	}
	env := alpacaEnv(validDevEnv())
	env["PRICE_PROVIDER"] = "  alpaca\t"
	if cfg, err := Load(envFrom(env)); err != nil || cfg.PriceProvider != ProviderAlpaca {
		t.Errorf("padded value: PriceProvider = %q, error = %v, want alpaca after trimming", cfg.PriceProvider, err)
	}
}

func TestAlpacaTradingBaseURLRules(t *testing.T) {
	accept := func(t *testing.T, env map[string]string, want string) {
		t.Helper()
		cfg, err := Load(envFrom(env))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.Alpaca.TradingBaseURL != want {
			t.Errorf("TradingBaseURL = %q, want %q", cfg.Alpaca.TradingBaseURL, want)
		}
	}
	t.Run("plain http only outside production", func(t *testing.T) {
		env := alpacaEnv(validDevEnv())
		env["ALPACA_TRADING_BASE_URL"] = "http://localhost:9999"
		accept(t, env, "http://localhost:9999")

		prod := validProdEnv()
		prod["ALPACA_TRADING_BASE_URL"] = "http://localhost:9999"
		if _, err := Load(envFrom(prod)); err == nil || !strings.Contains(err.Error(), "ALPACA_TRADING_BASE_URL") {
			t.Fatalf("Load() error = %v, want an https error in production", err)
		}
	})
	t.Run("a trailing slash is dropped", func(t *testing.T) {
		env := alpacaEnv(validDevEnv())
		env["ALPACA_TRADING_BASE_URL"] = "https://api.alpaca.markets/"
		accept(t, env, "https://api.alpaca.markets")
	})
	for _, bad := range []string{"https://paper-api.alpaca.markets/v2", "https://paper-api.alpaca.markets?x=1",
		"https://paper-api.alpaca.markets#frag", "https://user:pass@paper-api.alpaca.markets", "not a url", "ftp://host", "/v2"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			env := alpacaEnv(validDevEnv())
			env["ALPACA_TRADING_BASE_URL"] = bad
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), "ALPACA_TRADING_BASE_URL") {
				t.Fatalf("Load() error = %v, want ALPACA_TRADING_BASE_URL error", err)
			}
			if strings.Contains(err.Error(), "pass@") {
				t.Errorf("error %q echoes the credentials in the URL", err)
			}
		})
	}
}

func TestAlpacaSecretsAreRedacted(t *testing.T) {
	cfg, err := Load(envFrom(alpacaEnv(validDevEnv())))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, text := range []string{fmt.Sprintf("%v", cfg.Alpaca), fmt.Sprintf("%+v", cfg.Alpaca), fmt.Sprintf("%#v", cfg), fmt.Sprintf("%s", cfg.Alpaca.Key)} {
		if strings.Contains(text, testAlpacaKey) || strings.Contains(text, testAlpacaSecret) {
			t.Errorf("formatted config %q reveals an Alpaca credential", text)
		}
	}
}

func TestAlpacaDataBaseURLDefaultsToTheMarketDataAPI(t *testing.T) {
	cfg, err := Load(envFrom(alpacaEnv(validDevEnv())))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Alpaca.DataBaseURL != "https://data.alpaca.markets" {
		t.Errorf("DataBaseURL = %q, want https://data.alpaca.markets", cfg.Alpaca.DataBaseURL)
	}
	if demo, err := Load(envFrom(validDevEnv())); err != nil || demo.Alpaca.DataBaseURL != "" {
		t.Errorf("demo config: DataBaseURL = %q, error = %v; want it empty", demo.Alpaca.DataBaseURL, err)
	}
}

func TestAlpacaDataBaseURLRules(t *testing.T) {
	t.Run("plain http only outside production", func(t *testing.T) {
		env := alpacaEnv(validDevEnv())
		env["ALPACA_DATA_BASE_URL"] = "http://localhost:9998/"
		cfg, err := Load(envFrom(env))
		if err != nil || cfg.Alpaca.DataBaseURL != "http://localhost:9998" {
			t.Fatalf("Load() = %q, %v; want http://localhost:9998", cfg.Alpaca.DataBaseURL, err)
		}
		prod := validProdEnv()
		prod["ALPACA_DATA_BASE_URL"] = "http://localhost:9998"
		if _, err := Load(envFrom(prod)); err == nil || !strings.Contains(err.Error(), "ALPACA_DATA_BASE_URL") {
			t.Fatalf("Load() error = %v, want an https error naming ALPACA_DATA_BASE_URL", err)
		}
	})
	for _, bad := range []string{"https://data.alpaca.markets/v2", "https://data.alpaca.markets?x=1",
		"https://data.alpaca.markets#frag", "https://user:pass@data.alpaca.markets", "not a url", "ftp://host"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			env := alpacaEnv(validDevEnv())
			env["ALPACA_DATA_BASE_URL"] = bad
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), "ALPACA_DATA_BASE_URL") {
				t.Fatalf("Load() error = %v, want an ALPACA_DATA_BASE_URL error", err)
			}
			if strings.Contains(err.Error(), "ALPACA_TRADING_BASE_URL") || strings.Contains(err.Error(), "pass@") {
				t.Errorf("error %q names the wrong variable or echoes credentials", err)
			}
		})
	}
}
