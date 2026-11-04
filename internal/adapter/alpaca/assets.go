// Package alpaca is the secondary adapter for Alpaca: it verifies that a symbol is a US stock with
// the Assets API (and, in later milestones, reads prices). It uses net/http only.
package alpaca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

const (
	// DefaultTimeout limits one Assets API call (spec section 12).
	DefaultTimeout = 5 * time.Second
	// DefaultNegativeTTL is how long an unknown symbol is remembered (spec section 7).
	DefaultNegativeTTL = 10 * time.Minute
	// DefaultNegativeMax bounds the negative cache.
	DefaultNegativeMax = 1000

	maxResponseBytes = 64 << 10
	maxNameRunes     = 200
	maxExchangeRunes = 20
	maxAssetIDRunes  = 64
)

// AssetsConfig configures Assets. KeyID and Secret are the Alpaca API key pair; BaseURL is the Trading
// API origin such as https://paper-api.alpaca.markets.
type AssetsConfig struct {
	BaseURL     string
	KeyID       string
	Secret      string
	Timeout     time.Duration
	NegativeTTL time.Duration
	NegativeMax int
	Now         func() time.Time // nil means time.Now
	Client      *http.Client     // nil means a client whose timeout is Timeout
}

// Assets implements port.InstrumentProvider with GET /v2/assets/{symbol}.
type Assets struct {
	base    string
	keyID   string
	secret  string
	timeout time.Duration
	now     func() time.Time
	api     *apiClient
	unknown *negativeCache
}

var _ port.InstrumentProvider = (*Assets)(nil)

// NewAssets checks cfg. Its errors never contain the key or the secret.
func NewAssets(cfg AssetsConfig) (*Assets, error) {
	switch {
	case cfg.BaseURL == "":
		return nil, errors.New("alpaca assets: base URL is required")
	case cfg.KeyID == "" || cfg.Secret == "":
		return nil, errors.New("alpaca assets: key id and secret are required")
	case cfg.Timeout <= 0:
		return nil, errors.New("alpaca assets: timeout must be positive")
	case cfg.NegativeTTL <= 0 || cfg.NegativeMax < 1:
		return nil, errors.New("alpaca assets: negative cache needs a positive ttl and size")
	}
	a := &Assets{
		base: cfg.BaseURL, keyID: cfg.KeyID, secret: cfg.Secret, timeout: cfg.Timeout,
		now: cfg.Now, unknown: newNegativeCache(cfg.NegativeTTL, cfg.NegativeMax),
		// A symbol lookup is not retried (spec section 12): the user asks again.
		api: newAPIClient(cfg.KeyID, cfg.Secret, cfg.Timeout, cfg.Client, 0),
	}
	if a.now == nil {
		a.now = time.Now
	}
	return a, nil
}

// asset is the part of an Alpaca asset object that Lookup reads.
type asset struct {
	ID       string `json:"id"`
	Class    string `json:"class"`
	Exchange string `json:"exchange"`
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Status   string `json:"status"`
}

// Lookup asks Alpaca about symbol. A 404, or an asset that is not a us_equity, is
// domain.ErrUnknownSymbol and is remembered for the negative TTL. Any other failure (network,
// credentials, rate limit, a malformed answer) is an error the caller may retry and is never
// remembered; its text names the HTTP status but never the key, the secret or Alpaca's body.
func (a *Assets) Lookup(ctx context.Context, symbol domain.Symbol) (domain.Instrument, error) {
	key := symbol.String()
	if a.unknown.seen(key, a.now()) {
		return domain.Instrument{}, domain.ErrUnknownSymbol
	}
	status, raw, err := a.api.get(ctx, a.base+"/v2/assets/"+url.PathEscape(key), maxResponseBytes)
	if err != nil {
		return domain.Instrument{}, fmt.Errorf("alpaca assets: %w", err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		a.unknown.remember(key, a.now())
		return domain.Instrument{}, domain.ErrUnknownSymbol
	default:
		return domain.Instrument{}, statusError("alpaca assets", status)
	}
	var got asset
	if err := json.Unmarshal(raw, &got); err != nil {
		return domain.Instrument{}, errors.New("alpaca assets: answer is not a JSON asset")
	}
	return a.instrumentFrom(key, got)
}

func (a *Assets) instrumentFrom(requested string, got asset) (domain.Instrument, error) {
	if got.ID == "" || got.Class == "" || got.Exchange == "" || got.Symbol == "" || got.Name == "" || got.Status == "" {
		return domain.Instrument{}, errors.New("alpaca assets: answer is missing a required field")
	}
	if got.Class != string(domain.AssetClassUSEquity) {
		a.unknown.remember(requested, a.now())
		return domain.Instrument{}, domain.ErrUnknownSymbol
	}
	symbol, err := domain.ParseSymbol(got.Symbol)
	if err != nil {
		return domain.Instrument{}, domain.ErrUnknownSymbol
	}
	status, err := domain.ParseInstrumentStatus(got.Status)
	if err != nil {
		return domain.Instrument{}, errors.New("alpaca assets: answer has an unknown status")
	}
	name := got.Name
	if runes := []rune(name); len(runes) > maxNameRunes {
		name = string(runes[:maxNameRunes])
	}
	// The rules of domain.NewInstrument, checked here so that a well-formed but unexpected answer is an
	// error the caller can retry (503) and not an internal error (500) when the service stores it.
	if !printable(name, 1, maxNameRunes) || !printable(got.Exchange, 1, maxExchangeRunes) || !printable(got.ID, 1, maxAssetIDRunes) {
		return domain.Instrument{}, errors.New("alpaca assets: answer has a value the instrument rules refuse")
	}
	return domain.Instrument{
		Symbol: symbol, ProviderAssetID: got.ID, Name: name, Exchange: got.Exchange,
		AssetClass: domain.AssetClassUSEquity, Status: status, Source: domain.InstrumentSourceAlpaca,
	}, nil
}

// printable reports whether s is valid UTF-8 of min..max characters without control characters, the
// shape domain.NewInstrument requires of the name, the exchange and the provider asset ID.
func printable(s string, min, max int) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && n >= min && n <= max && strings.IndexFunc(s, unicode.IsControl) < 0
}
