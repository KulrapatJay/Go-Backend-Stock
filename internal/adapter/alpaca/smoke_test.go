package alpaca

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"go-backend-stock/internal/core/domain"
)

func decimalFromInt(n int64) decimal.Decimal { return decimal.NewFromInt(n) }

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

// TestAlpacaDailyBarsSmoke asks the real Market Data API for the daily bars of AAPL, BRK.B and SPY over
// the last month and over a stretch of 2020 that holds AAPL's 4:1 split of 31 August 2020. It checks what the
// plan and the tests assume of Alpaca: the free plan answers an end no newer than 15 minutes, the bars are
// unadjusted (the price before the split is about four times the price after it), every bar is dated by
// the New York day, and no bar of today is returned. Like the other smoke tests it runs only through
// `make smoke-alpaca`, and it prints no configuration value.
func TestAlpacaDailyBarsSmoke(t *testing.T) {
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

	today := domain.MarketDate(time.Now())
	// to is today on purpose: the adapter must cut it back to the last closed day instead of failing with 403.
	recent, err := prices.DailyBars(t.Context(), []domain.Symbol{mustSymbol(t, "AAPL"), mustSymbol(t, "BRK.B"), mustSymbol(t, "SPY")}, today.AddDays(-30), today)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP status 403") {
			t.Fatalf("DailyBars() error = %v: the account has no right to read historical SIP bars", err)
		}
		t.Fatalf("DailyBars() error = %v", err)
	}
	if recent.Source != domain.BarSourceAlpaca || recent.Feed != "sip" {
		t.Errorf("batch source/feed = %s/%s, want alpaca/sip", recent.Source, recent.Feed)
	}
	for _, sym := range []string{"AAPL", "BRK.B", "SPY"} {
		list := recent.Bars[sym]
		if len(list) < 10 {
			t.Errorf("%s has %d bars in the last 30 days, want at least 10", sym, len(list))
			continue
		}
		for i, b := range list {
			if !b.Date.Before(today) {
				t.Errorf("%s has a bar dated %s, which has not closed (today is %s)", sym, b.Date, today)
			}
			if i > 0 && !list[i-1].Date.Before(b.Date) {
				t.Errorf("%s bars are not oldest first at %s", sym, b.Date)
			}
			if wd := b.Date.UTC().Weekday(); wd == time.Saturday || wd == time.Sunday {
				t.Errorf("%s has a bar on a %s (%s): the date must be the New York day of the bar", sym, wd, b.Date)
			}
		}
	}

	split, err := prices.DailyBars(t.Context(), []domain.Symbol{mustSymbol(t, "AAPL")}, domain.NewTradingDate(2020, time.August, 27), domain.NewTradingDate(2020, time.September, 2))
	if err != nil {
		t.Fatalf("DailyBars(2020 split) error = %v", err)
	}
	list := split.Bars["AAPL"]
	var before, after domain.DailyBar
	for _, b := range list {
		switch b.Date.String() {
		case "2020-08-28":
			before = b
		case "2020-09-01":
			after = b
		}
	}
	if before.Date.IsZero() || after.Date.IsZero() {
		t.Fatalf("AAPL bars around the split = %d, want 2020-08-28 and 2020-09-01 among them", len(list))
	}
	if ratio := before.Close.Decimal().Div(after.Close.Decimal()); ratio.LessThan(decimalFromInt(3)) {
		t.Errorf("AAPL close before the split / after = %s, want about 4: the bars must be unadjusted (raw)", ratio)
	}

	// The oldest day the free plan serves: the start of the history assumed by ADR-0024.
	oldest, err := prices.DailyBars(t.Context(), []domain.Symbol{mustSymbol(t, "AAPL")}, domain.NewTradingDate(2015, time.December, 28), domain.NewTradingDate(2016, time.January, 8))
	if err != nil {
		t.Fatalf("DailyBars(2016) error = %v", err)
	}
	if got := oldest.Bars["AAPL"]; len(got) == 0 || got[0].Date.String() != "2016-01-04" {
		t.Errorf("first AAPL bar asked from 2015-12-28 = %v, want 2016-01-04 (the history of the free plan starts there)", got)
	}
}
