package domain

import "strings"

// PerformanceRange is how far back a performance report looks, counted from the last closed day (ADR-0025).
type PerformanceRange string

const (
	PerformanceRange1M  PerformanceRange = "1m"
	PerformanceRange3M  PerformanceRange = "3m"
	PerformanceRange6M  PerformanceRange = "6m"
	PerformanceRangeYTD PerformanceRange = "ytd"
	PerformanceRange1Y  PerformanceRange = "1y"
	PerformanceRange5Y  PerformanceRange = "5y"
	PerformanceRangeAll PerformanceRange = "all"
)

// ParsePerformanceRange accepts exactly the lower-case names above; field is "range".
func ParsePerformanceRange(s string) (PerformanceRange, error) {
	return parseEnum("range", s,
		PerformanceRange1M, PerformanceRange3M, PerformanceRange6M, PerformanceRangeYTD,
		PerformanceRange1Y, PerformanceRange5Y, PerformanceRangeAll)
}

// Start returns the first date of the range that ends on through. firstTrade is the date of the oldest
// trade (zero when there is none); only "all" uses it. No range reaches further back than MaxBarRangeDays,
// the longest span of bars one request may ask for.
func (r PerformanceRange) Start(through, firstTrade TradingDate) TradingDate {
	var start TradingDate
	switch r {
	case PerformanceRange1M:
		start = through.AddMonths(-1)
	case PerformanceRange3M:
		start = through.AddMonths(-3)
	case PerformanceRange6M:
		start = through.AddMonths(-6)
	case PerformanceRangeYTD:
		start = through.StartOfYear()
	case PerformanceRange1Y:
		start = through.AddMonths(-12)
	case PerformanceRange5Y:
		start = through.AddMonths(-60)
	default: // all
		start = firstTrade
		if start.IsZero() {
			start = through
		}
	}
	if oldest := through.AddDays(-(MaxBarRangeDays - 1)); start.Before(oldest) {
		start = oldest
	}
	return start
}

// Benchmark is an index a portfolio is compared with. It is tracked through an ETF, so its return leaves
// out dividends just as the portfolio's does.
type Benchmark struct {
	Symbol Symbol
	Label  string
}

var benchmarkLabels = []struct{ symbol, label string }{
	{"SPY", "S&P 500 (SPY)"},
	{"QQQ", "Nasdaq-100 (QQQ)"},
}

// ParseBenchmarks reads a comma-separated list such as "SPY,QQQ" (field "benchmarks"). The empty string is
// no benchmark. A name that is not offered, a repeated name, or a blank part (including spaces around a
// comma) is invalid_input. The order asked is kept.
func ParseBenchmarks(s string) ([]Benchmark, error) {
	out := []Benchmark{}
	if s == "" {
		return out, nil
	}
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		var found bool
		for _, b := range benchmarkLabels {
			if part != b.symbol {
				continue
			}
			if seen[part] {
				return nil, invalidInput("benchmarks", "must not repeat a symbol")
			}
			sym, err := ParseSymbol(b.symbol)
			if err != nil {
				return nil, invariant("benchmarks", "has a built-in symbol that does not parse")
			}
			seen[part] = true
			out = append(out, Benchmark{Symbol: sym, Label: b.label})
			found = true
			break
		}
		if !found {
			return nil, invalidInput("benchmarks", "must be a comma-separated list of SPY, QQQ")
		}
	}
	return out, nil
}
