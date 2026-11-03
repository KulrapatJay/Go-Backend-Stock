package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// InstrumentProvider checks that a symbol is a real US stock. It is the outbound port to the
// outside source of instruments (Alpaca Assets, or the demo list in demo mode).
type InstrumentProvider interface {
	// Lookup returns the instrument for symbol without ID or timestamps: the service adds those
	// before it stores the row. It returns domain.ErrUnknownSymbol when the provider has no such
	// symbol; any other error means the provider could not answer and the caller may try again.
	Lookup(ctx context.Context, symbol domain.Symbol) (domain.Instrument, error)
}
