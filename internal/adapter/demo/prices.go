package demo

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// demoPrices are sample prices in USD for the demo list. They are invented numbers, not market data.
var demoPrices = map[string]string{
	"AAPL": "200", "MSFT": "420", "NVDA": "130", "GOOGL": "175",
	"AMZN": "190", "META": "520", "TSLA": "250", "SPY": "540",
}

// Prices is the demo port.PriceProvider: fixed sample prices for the demo list, marked as demo data.
// It does not know the market calendar, so every quote says so (session unknown, not stale). It is
// never a stand-in for a real provider that failed.
type Prices struct {
	clock port.Clock
}

var _ port.PriceProvider = (*Prices)(nil)

// NewPrices returns the provider of the fixed demo prices. clock stamps FetchedAt.
func NewPrices(clock port.Clock) *Prices { return &Prices{clock: clock} }

// Quotes prices every symbol of the demo list as of the minute of cutoff; any other symbol is
// unavailable.
func (p *Prices) Quotes(ctx context.Context, symbols []domain.Symbol, cutoff time.Time) (domain.QuoteBatch, error) {
	if err := ctx.Err(); err != nil {
		return domain.QuoteBatch{}, err
	}
	asOf := cutoff.UTC().Truncate(time.Minute)
	fetchedAt := p.clock.Now().UTC()
	if fetchedAt.Before(asOf) {
		fetchedAt = asOf // a quote is never fetched before the data it holds
	}
	batch := domain.QuoteBatch{Quotes: make(map[string]domain.PriceQuote, len(symbols))}
	for _, symbol := range symbols {
		raw, ok := demoPrices[symbol.String()]
		if !ok {
			batch.Unavailable = append(batch.Unavailable, symbol)
			continue
		}
		price, err := domain.ParsePrice(raw)
		if err != nil {
			return domain.QuoteBatch{}, err
		}
		quote, err := domain.NewPriceQuote(domain.PriceQuote{
			Symbol: symbol, Price: price, Currency: "USD", AsOf: asOf, FetchedAt: fetchedAt,
			Source: "demo", Feed: "demo", IsDemo: true, SessionStatus: domain.SessionUnknown,
		})
		if err != nil {
			return domain.QuoteBatch{}, err
		}
		batch.Quotes[symbol.String()] = quote
	}
	return batch, nil
}
