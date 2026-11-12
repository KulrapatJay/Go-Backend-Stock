package port

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
)

// PriceBarRepository stores the daily bars asked from a price provider, so a chart does not wait for the
// provider every time (ADR-0024). The bars are a copy of market data, not business data: they have no
// owner and no audit event, and a row once written is never changed. Rows are kept per source, so bars of
// demo mode are never read as real ones. Every method reads or writes with its own statement or
// transaction; none takes part in a UnitOfWork.
type PriceBarRepository interface {
	// Coverage returns the range of dates already asked from the source for the instrument; ok is false
	// when it has never been asked.
	Coverage(ctx context.Context, instrumentID domain.ID, source domain.BarSource) (coverage domain.BarCoverage, ok bool, err error)
	// Range returns the stored bars with from <= date <= to, oldest first. Dates with no bar are left out.
	Range(ctx context.Context, instrumentID domain.ID, source domain.BarSource, from, to domain.TradingDate) ([]domain.DailyBar, error)
	// Coverages returns the coverage of each instrument that has one for the source, keyed by instrument.
	// An instrument never asked about is left out. An empty list returns an empty map without querying.
	Coverages(ctx context.Context, instrumentIDs []domain.ID, source domain.BarSource) (map[domain.ID]domain.BarCoverage, error)
	// RangeMany returns the stored bars of the instruments with from <= date <= to, keyed by instrument,
	// each list oldest first. An instrument with no bar in the range is left out.
	RangeMany(ctx context.Context, instrumentIDs []domain.ID, source domain.BarSource, from, to domain.TradingDate) (map[domain.ID][]domain.DailyBar, error)
	// Save stores bars, all dated inside fetched (a bar outside it is an internal error and nothing is
	// written), and widens the coverage to include fetched when fetched touches or overlaps it or the
	// coverage is new. A range that leaves a gap is not added to the coverage, so a gap is never recorded as
	// fetched; its bars are still stored. A bar already stored for the same date is kept as it is. It is
	// safe to call concurrently with the same arguments. now is the time the bars were fetched.
	Save(ctx context.Context, instrumentID domain.ID, source domain.BarSource, bars []domain.DailyBar, fetched domain.DateRange, now time.Time) error
}
