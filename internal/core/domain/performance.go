package domain

import (
	"sort"
	"strings"

	"github.com/shopspring/decimal"
)

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

// PerformancePoint is the portfolio on one day. MarketValue and ReturnPercent are nil together on a day a
// stock that was still held has no bar: a missing price is never valued as zero.
type PerformancePoint struct {
	Date          TradingDate
	MarketValue   *decimal.Decimal
	ReturnPercent *decimal.Decimal // cumulative since From, in percent
}

// BenchmarkPoint is the return of a benchmark on a day of the portfolio. ReturnPercent is nil when the
// benchmark has no bar that day or none on the first day.
type BenchmarkPoint struct {
	Date          TradingDate
	ReturnPercent *decimal.Decimal
}

// BenchmarkPerformance is a benchmark measured over the same days as the portfolio. ExcessReturnPercent
// is the portfolio return minus ReturnPercent, in percentage points.
type BenchmarkPerformance struct {
	Benchmark
	ReturnPercent       *decimal.Decimal
	ExcessReturnPercent *decimal.Decimal
	Points              []BenchmarkPoint // never nil
}

// Performance is the daily value and the time-weighted return of a portfolio, with benchmarks.
type Performance struct {
	From, To      TradingDate // the first and last point; zero when there is no point
	Coverage      Coverage
	ReturnPercent *decimal.Decimal       // that of the last point that has one
	Points        []PerformancePoint     // never nil, oldest first
	Benchmarks    []BenchmarkPerformance // never nil, in the order asked
}

// BenchmarkBars is the stored bars of one benchmark, oldest first.
type BenchmarkBars struct {
	Benchmark Benchmark
	Bars      []DailyBar
}

// PerformanceInput is everything ComputePerformance needs; it reads no store and calls no provider.
type PerformanceInput struct {
	Trades         []Transaction     // of any portfolios, in any order
	Bars           map[ID][]DailyBar // by instrument, oldest first
	Start, Through TradingDate
	Benchmarks     []BenchmarkBars
}

var hundredPercent = decimal.NewFromInt(100)

// ComputePerformance values the shares held at the end of each day that has a bar and chains the daily
// returns (ADR-0025). A day's return is (V + sells) / (V_before + buys) - 1: a buy counts at the start of
// the day and a sale at its end, both with their fee, so money moved in or out is not a return and the
// divisor is above zero whenever something was invested.
func ComputePerformance(in PerformanceInput) Performance {
	trades := tradesThrough(in.Trades, in.Through)

	closes := make(map[ID]map[string]decimal.Decimal, len(in.Bars))
	dateSet := map[string]TradingDate{}
	addDates := func(bars []DailyBar) {
		for _, b := range bars {
			if b.Date.Before(in.Start) || b.Date.After(in.Through) {
				continue
			}
			dateSet[b.Date.String()] = b.Date
		}
	}
	for id, bars := range in.Bars {
		m := make(map[string]decimal.Decimal, len(bars))
		for _, b := range bars {
			m[b.Date.String()] = b.Close.Decimal()
		}
		closes[id] = m
		addDates(bars)
	}
	for _, b := range in.Benchmarks {
		addDates(b.Bars)
	}
	dates := make([]TradingDate, 0, len(dateSet))
	for _, d := range dateSet {
		dates = append(dates, d)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })

	out := Performance{Points: []PerformancePoint{}, Benchmarks: make([]BenchmarkPerformance, 0, len(in.Benchmarks))}
	qty := map[ID]decimal.Decimal{}
	var bought, sold, prev, factor decimal.Decimal
	hasBase, next := false, 0
	for _, d := range dates {
		for ; next < len(trades) && !MarketDate(trades[next].ExecutedAt).After(d); next++ {
			applyFlow(qty, &bought, &sold, trades[next])
		}
		value, known := valueOf(qty, closes, d)
		switch {
		case !hasBase && known && value.IsPositive():
			hasBase = true
			out.From = d
			factor, prev = decimal.NewFromInt(1), value
			bought, sold = decimal.Zero, decimal.Zero
			out.Points = append(out.Points, point(d, value, factor))
		case !hasBase:
			bought, sold = decimal.Zero, decimal.Zero
		case !known:
			out.Points = append(out.Points, PerformancePoint{Date: d}) // bought and sold keep adding up
		default:
			if denom := prev.Add(bought); denom.IsPositive() {
				factor = factor.Mul(divideSigned(value.Add(sold), denom)).RoundBank(divisionPlaces)
			}
			out.Points = append(out.Points, point(d, value, factor))
			prev = value
			bought, sold = decimal.Zero, decimal.Zero
		}
	}

	var lastReturnDate TradingDate
	for _, p := range out.Points {
		out.To = p.Date
		if p.ReturnPercent != nil {
			out.ReturnPercent, lastReturnDate = p.ReturnPercent, p.Date
		}
	}
	switch {
	case len(out.Points) > 0:
		out.Coverage = CoverageComplete
		for _, p := range out.Points {
			if p.MarketValue == nil {
				out.Coverage = CoveragePartial
				break
			}
		}
	case len(InstrumentsHeldSince(trades, in.Start)) > 0:
		out.Coverage = CoverageUnavailable
	default:
		out.Coverage = CoverageComplete
	}

	for _, b := range in.Benchmarks {
		out.Benchmarks = append(out.Benchmarks, benchmarkOver(b, out, lastReturnDate))
	}
	return out
}

// InstrumentsHeldSince lists, in order of first appearance, the instruments held at the end of the day
// before from or traded on from or later: the ones whose bars a performance from that date needs.
func InstrumentsHeldSince(trades []Transaction, from TradingDate) []ID {
	sorted := sortedTrades(trades)
	before := map[ID]decimal.Decimal{}
	for _, tx := range sorted {
		if MarketDate(tx.ExecutedAt).Before(from) {
			before[tx.InstrumentID] = before[tx.InstrumentID].Add(signedQuantity(tx))
		}
	}
	out := []ID{}
	seen := map[ID]bool{}
	for _, tx := range sorted {
		if seen[tx.InstrumentID] {
			continue
		}
		if before[tx.InstrumentID].IsPositive() || !MarketDate(tx.ExecutedAt).Before(from) {
			seen[tx.InstrumentID] = true
			out = append(out, tx.InstrumentID)
		}
	}
	return out
}

// sortedTrades returns a copy ordered by (ExecutedAt, Sequence), the order the portfolio rules replay in.
func sortedTrades(trades []Transaction) []Transaction {
	out := append([]Transaction(nil), trades...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ExecutedAt.Equal(out[j].ExecutedAt) {
			return out[i].ExecutedAt.Before(out[j].ExecutedAt)
		}
		return out[i].Sequence < out[j].Sequence
	})
	return out
}

// tradesThrough sorts the trades and drops the ones dated after through.
func tradesThrough(trades []Transaction, through TradingDate) []Transaction {
	sorted := sortedTrades(trades)
	n := len(sorted)
	for n > 0 && MarketDate(sorted[n-1].ExecutedAt).After(through) {
		n--
	}
	return sorted[:n]
}

func signedQuantity(tx Transaction) decimal.Decimal {
	if tx.Side == SideSell {
		return tx.Quantity.Decimal().Neg()
	}
	return tx.Quantity.Decimal()
}

// applyFlow moves the shares and adds the money of one trade: a buy is quantity x price + fee paid in, a
// sale is quantity x price - fee paid out.
func applyFlow(qty map[ID]decimal.Decimal, bought, sold *decimal.Decimal, tx Transaction) {
	gross := tx.Quantity.Decimal().Mul(tx.Price.Decimal())
	qty[tx.InstrumentID] = qty[tx.InstrumentID].Add(signedQuantity(tx))
	if tx.Side == SideSell {
		*sold = sold.Add(gross.Sub(tx.Fee.Decimal()))
		return
	}
	*bought = bought.Add(gross.Add(tx.Fee.Decimal()))
}

// valueOf is the value of the shares held at the end of d. known is false when a stock still held has no
// bar on d.
func valueOf(qty map[ID]decimal.Decimal, closes map[ID]map[string]decimal.Decimal, d TradingDate) (value decimal.Decimal, known bool) {
	key := d.String()
	for id, q := range qty {
		if !q.IsPositive() {
			continue
		}
		c, ok := closes[id][key]
		if !ok {
			return decimal.Zero, false
		}
		value = value.Add(q.Mul(c))
	}
	return value, true
}

func point(d TradingDate, value, factor decimal.Decimal) PerformancePoint {
	r := factor.Sub(decimal.NewFromInt(1)).Mul(hundredPercent)
	return PerformancePoint{Date: d, MarketValue: &value, ReturnPercent: &r}
}

// divideSigned is divideHalfEven for a numerator of either sign; the divisor must be above zero.
func divideSigned(a, b decimal.Decimal) decimal.Decimal {
	if a.IsNegative() {
		return divideHalfEven(a.Neg(), b).Neg()
	}
	return divideHalfEven(a, b)
}

// benchmarkOver measures one benchmark on the days of the portfolio's points, from its close on the first.
func benchmarkOver(b BenchmarkBars, p Performance, lastReturnDate TradingDate) BenchmarkPerformance {
	out := BenchmarkPerformance{Benchmark: b.Benchmark, Points: make([]BenchmarkPoint, 0, len(p.Points))}
	closes := make(map[string]decimal.Decimal, len(b.Bars))
	for _, bar := range b.Bars {
		closes[bar.Date.String()] = bar.Close.Decimal()
	}
	base, hasBase := closes[p.From.String()]
	for _, pt := range p.Points {
		var r *decimal.Decimal
		if c, ok := closes[pt.Date.String()]; ok && hasBase {
			v := divideHalfEven(c, base).Sub(decimal.NewFromInt(1)).Mul(hundredPercent)
			r = &v
		}
		out.Points = append(out.Points, BenchmarkPoint{Date: pt.Date, ReturnPercent: r})
		if pt.Date.Equal(lastReturnDate) {
			out.ReturnPercent = r
		}
	}
	if out.ReturnPercent != nil && p.ReturnPercent != nil {
		excess := p.ReturnPercent.Sub(*out.ReturnPercent)
		out.ExcessReturnPercent = &excess
	}
	return out
}
