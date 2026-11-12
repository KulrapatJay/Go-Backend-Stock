package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// PerformanceQuery says what a performance report covers: how far back it looks and which benchmarks it is
// measured against (none when the list is empty).
type PerformanceQuery struct {
	Range      domain.PerformanceRange
	Benchmarks []domain.Benchmark
}

// PerformanceReport is a computed domain.Performance with where its prices came from. DataThrough is the
// last day every stock it needed has stored bars for, never later than the last closed day; it is zero when
// nothing was needed.
type PerformanceReport struct {
	domain.Performance
	Source      domain.BarSource
	DataThrough domain.TradingDate
}

// PerformanceService is the inbound port for the daily value and the time-weighted return of a portfolio or
// of the whole account (ADR-0025). It needs an authenticated principal (a zero one returns
// domain.ErrUnauthenticated).
type PerformanceService interface {
	// Portfolio reports one of the caller's portfolios; one that is not theirs is domain.ErrNotFound.
	Portfolio(ctx context.Context, p domain.Principal, portfolioID domain.ID, q PerformanceQuery, meta domain.RequestMeta) (PerformanceReport, error)
	// Account reports every portfolio of the caller added together.
	//
	// Both ask the price provider for the bars the store lacks, with no transaction and no lock held. When
	// the provider cannot answer, the stored bars are used (Coverage is then at most partial) if every stock
	// needed has some; otherwise the error is domain.ErrDependencyUnavailable. A benchmark the store has not
	// seen is checked with the instrument provider within the per-user lookup limit.
	Account(ctx context.Context, p domain.Principal, q PerformanceQuery, meta domain.RequestMeta) (PerformanceReport, error)
}
