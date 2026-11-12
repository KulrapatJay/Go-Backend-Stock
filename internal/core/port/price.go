package port

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
)

// PriceProvider is the source of delayed reference prices.
type PriceProvider interface {
	// Quotes returns, for every symbol it can price, the close of the last bar that ended at or before
	// cutoff. A symbol it has no price for goes in QuoteBatch.Unavailable and is not an error. It
	// returns an error only when the provider itself could not be asked or did not answer, and the
	// caller then treats every symbol as unpriced; an adapter never substitutes sample data for a real
	// provider's failure.
	Quotes(ctx context.Context, symbols []domain.Symbol, cutoff time.Time) (domain.QuoteBatch, error)
	// DailyBars returns the unadjusted daily bars of symbols whose date lies in from..to, oldest first. A
	// symbol with no bar in the range (a holiday-only range, a stock not yet listed) has an empty list; that
	// is not an error. A bar of a day that has not closed yet is never returned: the provider drops any bar
	// dated today or later in New York. It returns an error only when the provider itself could not be asked
	// or did not answer, and an adapter never substitutes sample data for a real provider's failure.
	DailyBars(ctx context.Context, symbols []domain.Symbol, from, to domain.TradingDate) (domain.DailyBarBatch, error)
}
