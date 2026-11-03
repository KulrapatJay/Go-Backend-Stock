// Package demo holds the adapters that answer from fixed sample data. They exist only for the demo
// mode (PRICE_PROVIDER=demo) and for tests; production configuration rejects them.
package demo

import (
	"context"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// Instruments is the demo port.InstrumentProvider: it knows a short fixed list of US stocks and
// reports every other symbol as unknown. It never falls back to, or stands in for, a real provider.
type Instruments struct {
	known map[string]entry
}

type entry struct{ name, exchange string }

var _ port.InstrumentProvider = (*Instruments)(nil)

// NewInstruments returns the provider of the fixed demo list.
func NewInstruments() *Instruments {
	return &Instruments{known: map[string]entry{
		"AAPL":  {"Apple Inc.", "NASDAQ"},
		"MSFT":  {"Microsoft Corporation", "NASDAQ"},
		"NVDA":  {"NVIDIA Corporation", "NASDAQ"},
		"GOOGL": {"Alphabet Inc. Class A", "NASDAQ"},
		"AMZN":  {"Amazon.com, Inc.", "NASDAQ"},
		"META":  {"Meta Platforms, Inc.", "NASDAQ"},
		"TSLA":  {"Tesla, Inc.", "NASDAQ"},
		"SPY":   {"SPDR S&P 500 ETF Trust", "ARCA"},
	}}
}

// Lookup returns the demo instrument for symbol, without ID or timestamps (the service adds them),
// or domain.ErrUnknownSymbol.
func (p *Instruments) Lookup(ctx context.Context, symbol domain.Symbol) (domain.Instrument, error) {
	if err := ctx.Err(); err != nil {
		return domain.Instrument{}, err
	}
	e, ok := p.known[symbol.String()]
	if !ok {
		return domain.Instrument{}, domain.ErrUnknownSymbol
	}
	return domain.Instrument{
		Symbol: symbol, Name: e.name, Exchange: e.exchange, AssetClass: domain.AssetClassUSEquity,
		Status: domain.InstrumentStatusActive, Source: domain.InstrumentSourceDemo,
	}, nil
}
