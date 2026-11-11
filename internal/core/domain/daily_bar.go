package domain

import (
	"strings"

	"github.com/shopspring/decimal"
)

// What a client is told about daily bars. Phase 1 stores one kind: unadjusted daily bars (ADR-0024).
const (
	BarTimeframeDaily = "1d"
	BarAdjustmentRaw  = "raw"
	// MaxBarRangeDays is the longest from..to a client may ask for in one call (about ten years, the
	// history the free Alpaca plan gives).
	MaxBarRangeDays = 3660
)

// maxVolumeDigits keeps a volume inside numeric(28,0) with room to spare.
const maxVolumeDigits = 18

// Volume is a number of shares traded in a bar: a whole number, zero or greater. The zero value is a
// valid volume of 0.
type Volume struct{ value decimal.Decimal }

// ParseVolume parses digits only ("29910098"): no sign, fraction, exponent or separator.
func ParseVolume(s string) (Volume, error) {
	if !isDigits(s) {
		return Volume{}, invalidInput("volume", "must be a whole number of shares written with digits only")
	}
	if len(strings.TrimLeft(s, "0")) > maxVolumeDigits {
		return Volume{}, invalidInput("volume", "must have at most 18 digits")
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		// Unreachable after the checks above; never wrap err because it repeats the input.
		return Volume{}, invalidInput("volume", "must be a whole number of shares written with digits only")
	}
	return Volume{value: d}, nil
}

// Decimal returns the value for arithmetic.
func (v Volume) Decimal() decimal.Decimal { return v.value }

// String returns the number without exponent ("0" for the zero value).
func (v Volume) String() string { return v.value.String() }

// BarSource says which provider a stored bar came from. It is part of the key of stored bars, so sample
// bars made in demo mode can never be served as real prices after a switch to Alpaca.
type BarSource string

const (
	BarSourceAlpaca BarSource = "alpaca"
	BarSourceDemo   BarSource = "demo"
)

// ParseBarSource accepts exactly "alpaca" or "demo".
func ParseBarSource(s string) (BarSource, error) {
	return parseEnum("source", s, BarSourceAlpaca, BarSourceDemo)
}

// Feed names the feed the bars of the source come from, as a client is told: the delayed SIP bars of the free
// Alpaca plan, or "demo" for sample data.
func (s BarSource) Feed() string {
	if s == BarSourceAlpaca {
		return "sip"
	}
	return string(s)
}

// DailyBar is one trading day of one stock: unadjusted prices in USD and the shares traded. Date is
// the market date in New York.
type DailyBar struct {
	Date                   TradingDate
	Open, High, Low, Close Price
	Volume                 Volume
}

// NewDailyBar checks a bar an adapter or a stored row built. A violation is a bug in the adapter or
// corrupt data, so it returns an internal error. It requires only what the database enforces: every
// price above zero and high not below low; a bar whose open or close lies outside high..low is kept as
// the provider sent it.
func NewDailyBar(b DailyBar) (DailyBar, error) {
	var err error
	switch {
	case b.Date.IsZero():
		err = invariant("bar.date", "is required")
	case b.Open.IsZero():
		err = invariant("bar.open", "must be greater than zero")
	case b.High.IsZero():
		err = invariant("bar.high", "must be greater than zero")
	case b.Low.IsZero():
		err = invariant("bar.low", "must be greater than zero")
	case b.Close.IsZero():
		err = invariant("bar.close", "must be greater than zero")
	case b.High.Decimal().LessThan(b.Low.Decimal()):
		err = invariant("bar.high", "must not be below bar.low")
	}
	if err != nil {
		return DailyBar{}, err
	}
	return b, nil
}

// DailyBarBatch is a provider's answer for several symbols at once. Bars is keyed by Symbol.String(),
// each list oldest first with one bar per trading day. A symbol with no bar in the range has an empty
// list or no key; that is not an error. Source and Feed say where the bars came from.
type DailyBarBatch struct {
	Bars   map[string][]DailyBar
	Source BarSource
	Feed   string
}

// IsDemo reports whether the bars are sample data.
func (b DailyBarBatch) IsDemo() bool { return b.Source == BarSourceDemo }

// DateRange is a first and last date, both included.
type DateRange struct {
	From, To TradingDate
}

// BarCoverage is the one unbroken range of dates a provider has been asked about for a stock. A day
// inside it with no stored bar was a day without trading (a holiday), not a day never fetched.
type BarCoverage struct {
	From, Through TradingDate
}

// ExtendedBy returns the coverage that also includes from..to. The caller only asks for ranges that
// touch the covered one, so the result is still unbroken.
func (c BarCoverage) ExtendedBy(from, to TradingDate) BarCoverage {
	if from.Before(c.From) {
		c.From = from
	}
	if to.After(c.Through) {
		c.Through = to
	}
	return c
}

// MissingRanges returns what to ask the provider for so that coverage includes from..to and stays
// unbroken: nothing when from..to lies inside it; otherwise the part before it and the part after it,
// each reaching up to the covered range even when that asks for a few more days than were requested.
// A nil coverage asks for from..to.
func MissingRanges(coverage *BarCoverage, from, to TradingDate) []DateRange {
	if coverage == nil {
		return []DateRange{{From: from, To: to}}
	}
	var out []DateRange
	if from.Before(coverage.From) {
		out = append(out, DateRange{From: from, To: coverage.From.AddDays(-1)})
	}
	if to.After(coverage.Through) {
		out = append(out, DateRange{From: coverage.Through.AddDays(1), To: to})
	}
	return out
}

// DailyBarSeries is the answer to a request for the daily bars of one stock. Bars is oldest first.
// DataFrom and DataThrough are the first and last date the stored data covers (zero when it covers
// nothing); they may be earlier or later than the dates asked for.
type DailyBarSeries struct {
	Symbol         Symbol
	InstrumentName string
	Source         BarSource
	Feed           string
	Bars           []DailyBar
	DataFrom       TradingDate
	DataThrough    TradingDate
}

// IsDemo reports whether the bars are sample data.
func (s DailyBarSeries) IsDemo() bool { return s.Source == BarSourceDemo }
