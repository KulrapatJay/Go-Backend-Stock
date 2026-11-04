package alpaca

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// TestAlpacaSmoke asks the real Alpaca Assets API about three symbols. It runs only through
// `make smoke-alpaca`, with the key pair of a free paper account taken from the environment, and is
// skipped everywhere else: CI and `make check` never call Alpaca. It prints no configuration value.
func TestAlpacaSmoke(t *testing.T) {
	if os.Getenv("REQUIRE_ALPACA_SMOKE") == "" {
		t.Skip("set REQUIRE_ALPACA_SMOKE=1 (make smoke-alpaca) to call the real Alpaca Assets API")
	}
	keyID, secret := os.Getenv("ALPACA_KEY"), os.Getenv("ALPACA_SECRET")
	if keyID == "" || secret == "" {
		t.Fatal("ALPACA_KEY and ALPACA_SECRET must be set in .env (a free Alpaca paper account)")
	}
	base := os.Getenv("ALPACA_TRADING_BASE_URL")
	if base == "" {
		base = "https://paper-api.alpaca.markets"
	}
	assets, err := NewAssets(AssetsConfig{
		BaseURL: base, KeyID: keyID, Secret: secret,
		Timeout: DefaultTimeout, NegativeTTL: DefaultNegativeTTL, NegativeMax: DefaultNegativeMax,
	})
	if err != nil {
		t.Fatalf("NewAssets() error = %v", err)
	}

	aapl, err := assets.Lookup(t.Context(), mustSymbol(t, "AAPL"))
	if err != nil {
		t.Fatalf("Lookup(AAPL) error = %v", err)
	}
	if aapl.AssetClass != domain.AssetClassUSEquity || aapl.Status != domain.InstrumentStatusActive || aapl.ProviderAssetID == "" {
		t.Errorf("Lookup(AAPL) = class %q, status %q, asset id set: %t; want an active us_equity with an asset id",
			aapl.AssetClass, aapl.Status, aapl.ProviderAssetID != "")
	}

	// A symbol with a dot: the form the API takes for a share class.
	if brk, err := assets.Lookup(t.Context(), mustSymbol(t, "BRK.B")); err != nil || brk.Symbol.String() != "BRK.B" {
		t.Errorf("Lookup(BRK.B) = %q, %v; want BRK.B", brk.Symbol.String(), err)
	}

	if _, err := assets.Lookup(t.Context(), mustSymbol(t, "ZZZZZZZZ")); !errors.Is(err, domain.ErrUnknownSymbol) {
		t.Errorf("Lookup(ZZZZZZZZ) error = %v, want unknown_symbol", err)
	}
}

// TestAlpacaPricesSmoke asks the real Market Data API for delayed SIP minute bars and the Trading API
// for the market calendar. Like TestAlpacaSmoke it runs only through `make smoke-alpaca`, with the key
// of a free account taken from the environment, and it prints no configuration value.
func TestAlpacaPricesSmoke(t *testing.T) {
	if os.Getenv("REQUIRE_ALPACA_SMOKE") == "" {
		t.Skip("set REQUIRE_ALPACA_SMOKE=1 (make smoke-alpaca) to call the real Alpaca Market Data API")
	}
	keyID, secret := os.Getenv("ALPACA_KEY"), os.Getenv("ALPACA_SECRET")
	if keyID == "" || secret == "" {
		t.Fatal("ALPACA_KEY and ALPACA_SECRET must be set in .env (a free Alpaca paper account)")
	}
	tradingURL, dataURL := os.Getenv("ALPACA_TRADING_BASE_URL"), os.Getenv("ALPACA_DATA_BASE_URL")
	if tradingURL == "" {
		tradingURL = "https://paper-api.alpaca.markets"
	}
	if dataURL == "" {
		dataURL = "https://data.alpaca.markets"
	}
	prices, err := NewPrices(PricesConfig{
		DataBaseURL: dataURL, TradingBaseURL: tradingURL, KeyID: keyID, Secret: secret, Timeout: DefaultTimeout,
	})
	if err != nil {
		t.Fatalf("NewPrices() error = %v", err)
	}

	cutoff := time.Now().UTC().Add(-15 * time.Minute)
	batch, err := prices.Quotes(t.Context(), []domain.Symbol{mustSymbol(t, "AAPL"), mustSymbol(t, "SPY")}, cutoff)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP status 403") {
			t.Fatalf("Quotes() error = %v: the account has no right to read historical SIP bars", err)
		}
		t.Fatalf("Quotes() error = %v", err)
	}
	for _, sym := range []string{"AAPL", "SPY"} {
		q, ok := batch.Quotes[sym]
		if !ok {
			t.Errorf("Quotes() has no %s; unavailable = %v", sym, batch.Unavailable)
			continue
		}
		if q.Price.IsZero() || q.Feed != "sip" || q.IsDemo || q.AsOf.After(cutoff) {
			t.Errorf("%s = feed %q, demo %t, as_of after cutoff %t, price set %t; want a sip price at or before the cutoff",
				sym, q.Feed, q.IsDemo, q.AsOf.After(cutoff), !q.Price.IsZero())
		}
		if q.SessionStatus == domain.SessionUnknown {
			t.Errorf("%s session_status = unknown; the market calendar could not be read", sym)
		}
	}
}
