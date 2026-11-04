package alpaca

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PricesConfig configures Prices. DataBaseURL is the Market Data API origin (bars) and TradingBaseURL
// the Trading API origin (the market calendar).
type PricesConfig struct {
	DataBaseURL    string
	TradingBaseURL string
	KeyID          string
	Secret         string
	Timeout        time.Duration
	Now            func() time.Time // nil means time.Now
	Client         *http.Client     // nil means a client that follows no redirect
}

// Prices implements port.PriceProvider with delayed SIP minute bars from Alpaca. The reference price of
// a symbol is the close of the last finished bar of the regular session before the cutoff (see
// pickReference). It never substitutes another source when Alpaca fails.
type Prices struct {
	data   string
	client *apiClient
	cal    *calendar
	now    func() time.Time
}

var _ port.PriceProvider = (*Prices)(nil)

// NewPrices checks cfg. Its errors never contain the key or the secret.
func NewPrices(cfg PricesConfig) (*Prices, error) {
	switch {
	case cfg.DataBaseURL == "" || cfg.TradingBaseURL == "":
		return nil, errors.New("alpaca prices: data and trading base URLs are required")
	case cfg.KeyID == "" || cfg.Secret == "":
		return nil, errors.New("alpaca prices: key id and secret are required")
	case cfg.Timeout <= 0:
		return nil, errors.New("alpaca prices: timeout must be positive")
	}
	p := &Prices{data: cfg.DataBaseURL, now: cfg.Now}
	if p.now == nil {
		p.now = time.Now
	}
	// Market data and the calendar are read-only and safe to ask twice: one retry.
	p.client = newAPIClient(cfg.KeyID, cfg.Secret, cfg.Timeout, cfg.Client, 1)
	p.cal = newCalendar(p.client, cfg.TradingBaseURL, p.now)
	return p, nil
}

// Quotes prices symbols as of cutoff. A calendar that cannot be read does not stop it: the prices then
// say session unknown and stale. Bars that cannot be read are an error, and no price is invented. A
// symbol with no usable bar, or a close domain.ParsePrice refuses (an exponent, more than ten
// decimals), is Unavailable: a number is never rounded to fit.
func (p *Prices) Quotes(ctx context.Context, symbols []domain.Symbol, cutoff time.Time) (domain.QuoteBatch, error) {
	asked := uniqueSymbols(symbols)
	batch := domain.QuoteBatch{Quotes: make(map[string]domain.PriceQuote, len(asked))}
	if len(asked) == 0 {
		return batch, nil
	}
	sessions, err := p.cal.sessions(ctx, cutoff)
	if err != nil {
		if ctx.Err() != nil {
			return domain.QuoteBatch{}, fmt.Errorf("alpaca prices: %w", ctx.Err())
		}
		sessions = nil // no calendar: do not guess which days were trading days
	}
	start, end, ok := requestWindow(sessions, cutoff)
	if !ok {
		batch.Unavailable = asked
		return batch, nil
	}
	names := make([]string, len(asked))
	for i, s := range asked {
		names[i] = s.String()
	}
	bars, err := readBars(ctx, p.client, p.data, names, start, end)
	if err != nil {
		return domain.QuoteBatch{}, err
	}
	fetchedAt := p.now().UTC()
	for _, symbol := range asked {
		quote, priced := p.quoteFor(symbol, bars[symbol.String()], sessions, cutoff, fetchedAt)
		if !priced {
			batch.Unavailable = append(batch.Unavailable, symbol)
			continue
		}
		batch.Quotes[symbol.String()] = quote
	}
	return batch, nil
}

func (p *Prices) quoteFor(symbol domain.Symbol, bars []bar, sessions []session, cutoff, fetchedAt time.Time) (domain.PriceQuote, bool) {
	ref, found := pickReference(bars, sessions, cutoff)
	if !found {
		return domain.PriceQuote{}, false
	}
	price, err := domain.ParsePrice(ref.Bar.Close)
	if err != nil {
		return domain.PriceQuote{}, false
	}
	if fetchedAt.Before(ref.AsOf) {
		fetchedAt = ref.AsOf // a quote is never fetched before the data it holds
	}
	quote, err := domain.NewPriceQuote(domain.PriceQuote{
		Symbol: symbol, Price: price, Currency: "USD", AsOf: ref.AsOf, FetchedAt: fetchedAt,
		Source: "alpaca", Feed: "sip", Stale: ref.Stale, SessionStatus: ref.Status,
	})
	if err != nil {
		return domain.PriceQuote{}, false
	}
	return quote, true
}

// uniqueSymbols drops repeats and keeps the order.
func uniqueSymbols(symbols []domain.Symbol) []domain.Symbol {
	seen := make(map[string]bool, len(symbols))
	out := make([]domain.Symbol, 0, len(symbols))
	for _, s := range symbols {
		if !seen[s.String()] {
			seen[s.String()] = true
			out = append(out, s)
		}
	}
	return out
}
