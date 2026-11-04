package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ProviderName selects where instruments (and, later, prices) come from.
type ProviderName string

const (
	// ProviderDemo uses fixed sample data. Production refuses it.
	ProviderDemo ProviderName = "demo"
	// ProviderAlpaca uses the Alpaca APIs and needs ALPACA_KEY and ALPACA_SECRET.
	ProviderAlpaca ProviderName = "alpaca"
)

// defaultAlpacaTradingBaseURL is the paper trading endpoint: the Assets API is part of the Trading
// API, and a paper key is all the free plan needs.
const defaultAlpacaTradingBaseURL = "https://paper-api.alpaca.markets"

// defaultAlpacaDataBaseURL is the Market Data API, a different host from the Trading API.
const defaultAlpacaDataBaseURL = "https://data.alpaca.markets"

// AlpacaConfig holds the Alpaca credentials. It is empty unless PRICE_PROVIDER is alpaca.
type AlpacaConfig struct {
	Key            Secret
	Secret         Secret
	TradingBaseURL string
	DataBaseURL    string
}

// loadProvider reads PRICE_PROVIDER and ALPACA_*. Its errors name the variables but never their values.
func loadProvider(getenv func(string) string, prod bool) (ProviderName, AlpacaConfig, []error) {
	name := ProviderName(strings.TrimSpace(getenv("PRICE_PROVIDER")))
	if name == "" && !prod {
		name = ProviderDemo
	}
	switch {
	case name == ProviderDemo && !prod:
		return ProviderDemo, AlpacaConfig{}, nil
	case name == ProviderAlpaca:
	case prod:
		return "", AlpacaConfig{}, []error{errors.New("PRICE_PROVIDER must be alpaca in production")}
	default:
		return "", AlpacaConfig{}, []error{errors.New("PRICE_PROVIDER must be demo or alpaca")}
	}

	var errs []error
	cfg := AlpacaConfig{
		Key:    Secret(strings.TrimSpace(getenv("ALPACA_KEY"))),
		Secret: Secret(strings.TrimSpace(getenv("ALPACA_SECRET"))),
	}
	for _, v := range []struct {
		name  string
		value Secret
	}{{"ALPACA_KEY", cfg.Key}, {"ALPACA_SECRET", cfg.Secret}} {
		if v.value == "" {
			errs = append(errs, fmt.Errorf("%s is required when PRICE_PROVIDER is alpaca", v.name))
		}
	}
	base, err := parseAlpacaBaseURL("ALPACA_TRADING_BASE_URL", getenv("ALPACA_TRADING_BASE_URL"), defaultAlpacaTradingBaseURL, prod)
	if err != nil {
		errs = append(errs, err)
	}
	data, err := parseAlpacaBaseURL("ALPACA_DATA_BASE_URL", getenv("ALPACA_DATA_BASE_URL"), defaultAlpacaDataBaseURL, prod)
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return "", AlpacaConfig{}, errs
	}
	cfg.TradingBaseURL, cfg.DataBaseURL = base, data
	return ProviderAlpaca, cfg, nil
}

// parseAlpacaBaseURL accepts an origin only (scheme and host, optional trailing slash) and returns it
// without the slash; an empty value is fallback. Production needs https. The error names the variable
// and never echoes the value, which could hold credentials.
func parseAlpacaBaseURL(name, raw, fallback string, prod bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%s must be an origin such as %s", name, fallback)
	}
	if prod && u.Scheme != "https" {
		return "", fmt.Errorf("%s must use https in production", name)
	}
	return u.Scheme + "://" + u.Host, nil
}
