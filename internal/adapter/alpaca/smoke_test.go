package alpaca

import (
	"errors"
	"os"
	"testing"

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
