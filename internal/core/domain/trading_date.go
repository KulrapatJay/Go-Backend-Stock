package domain

import (
	"time"
	_ "time/tzdata" // the market zone must exist even where the OS has no zoneinfo (alpine, distroless)
)

// marketLocation is the zone of the US stock market. Daily bars are labelled with the date there.
var marketLocation = mustLoadLocation("America/New_York")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("domain: cannot load " + name + ": " + err.Error())
	}
	return loc
}

// minTradingYear keeps the date parser away from the zero time.Time, which would read as "no date".
const minTradingYear = 1970

// TradingDate is a calendar date of the US market (YYYY-MM-DD), not an instant: it has no time and no
// zone, so it cannot move with the timezone of a client. The zero value means "no date".
type TradingDate struct{ day time.Time } // midnight UTC of the date

// ParseTradingDate parses YYYY-MM-DD exactly: four-digit year (1970 or later), two-digit month and day,
// a date that exists. field names the query parameter in the error message.
func ParseTradingDate(field, s string) (TradingDate, error) {
	if len(s) != len("2006-01-02") || s[4] != '-' || s[7] != '-' ||
		!isDigits(s[:4]) || !isDigits(s[5:7]) || !isDigits(s[8:]) {
		return TradingDate{}, invalidInput(field, "must be a date written YYYY-MM-DD, such as 2026-11-10")
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil || t.Year() < minTradingYear {
		// Never wrap err: it repeats the input.
		return TradingDate{}, invalidInput(field, "must be a real date from 1970-01-01 written YYYY-MM-DD")
	}
	return TradingDate{day: t}, nil
}

// NewTradingDate returns the date y-m-d, normalized like time.Date (31 February becomes 3 March).
func NewTradingDate(y int, m time.Month, d int) TradingDate {
	return TradingDate{day: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// MarketDate returns the date of at in New York, the date a US daily bar is labelled with.
func MarketDate(at time.Time) TradingDate {
	y, m, d := at.In(marketLocation).Date()
	return NewTradingDate(y, m, d)
}

// String returns YYYY-MM-DD, or "" for the zero value.
func (d TradingDate) String() string {
	if d.IsZero() {
		return ""
	}
	return d.day.Format(time.DateOnly)
}

// IsZero reports whether d is the zero value, which is no date.
func (d TradingDate) IsZero() bool { return d.day.IsZero() }

// Before reports whether d is an earlier date than o.
func (d TradingDate) Before(o TradingDate) bool { return d.day.Before(o.day) }

// After reports whether d is a later date than o.
func (d TradingDate) After(o TradingDate) bool { return d.day.After(o.day) }

// Equal reports whether d and o are the same date.
func (d TradingDate) Equal(o TradingDate) bool { return d.day.Equal(o.day) }

// AddDays returns the date n calendar days later (earlier when n is negative).
func (d TradingDate) AddDays(n int) TradingDate {
	return TradingDate{day: d.day.AddDate(0, 0, n)}
}

// AddMonths returns the date n calendar months later (earlier when n is negative). A day the target
// month does not have is cut to its last day: 31 March minus one month is the last day of February.
func (d TradingDate) AddMonths(n int) TradingDate {
	y, m, day := d.day.Date()
	first := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	if day > last {
		day = last
	}
	return TradingDate{day: time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)}
}

// StartOfYear returns 1 January of the year of d.
func (d TradingDate) StartOfYear() TradingDate {
	return NewTradingDate(d.day.Year(), time.January, 1)
}

// DaysUntil returns how many calendar days after d the date o is (negative when o is earlier).
func (d TradingDate) DaysUntil(o TradingDate) int {
	return int(o.day.Sub(d.day).Hours() / 24)
}

// UTC returns midnight UTC of the date, for storage and comparison. It is not the start of the
// market day; use MarketDayStart for that.
func (d TradingDate) UTC() time.Time { return d.day }

// MarketDayStart returns the instant the date begins in New York (midnight there), in UTC. A provider
// that stamps a daily bar with that instant is stamping this date.
func (d TradingDate) MarketDayStart() time.Time {
	y, m, day := d.day.Date()
	return time.Date(y, m, day, 0, 0, 0, 0, marketLocation).UTC()
}
