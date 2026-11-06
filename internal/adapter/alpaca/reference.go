package alpaca

import (
	"time"

	"go-backend-stock/internal/core/domain"
)

const (
	barLength  = time.Minute
	staleAfter = 5 * time.Minute
	// noCalendarLookback is how far back bars are asked for when the calendar cannot be read: enough
	// for a long weekend.
	noCalendarLookback = 96 * time.Hour
)

// bar is one minute bar. Start is the left edge of its interval (Alpaca's t): the bar ends one
// minute later. Close is the c field exactly as Alpaca wrote it.
type bar struct {
	Start time.Time
	Close string
}

// reference is the bar chosen as the price of a symbol, and what is known about it. AsOf is the end of
// the bar.
type reference struct {
	Bar    bar
	AsOf   time.Time
	Status domain.SessionStatus
	Stale  bool
}

// window is the part of the trading day a price may come from: bars that start at or after Open and
// end at or before End. Expected is the time the newest possible bar would end.
type window struct {
	Open     time.Time
	End      time.Time // bars must end at or before this
	Expected time.Time
	Status   domain.SessionStatus
}

// referenceWindow decides which session the price comes from. A cutoff inside a session uses that
// session up to the minute of the cutoff and is "open"; any other cutoff (weekend, holiday, before the
// open, after the close, early close) uses the last session that has closed and is "market_closed",
// valued at its close. It reports false when the calendar knows no session that has started.
func referenceWindow(sessions []session, cutoff time.Time) (window, bool) {
	cutoffMinute := cutoff.UTC().Truncate(barLength)
	var last *session
	for i := range sessions {
		s := &sessions[i]
		switch {
		case !cutoff.Before(s.Open) && cutoff.Before(s.Close):
			return window{Open: s.Open, End: cutoffMinute, Expected: cutoffMinute, Status: domain.SessionOpen}, true
		case !s.Close.After(cutoff):
			last = s
		}
	}
	if last == nil {
		return window{}, false
	}
	return window{Open: last.Open, End: last.Close, Expected: last.Close, Status: domain.SessionMarketClosed}, true
}

// requestWindow is the range of bar start times to ask Alpaca for: from the session open to the last
// minute that can have finished. Without a calendar it is the last four days. ok is false when no bar
// can have finished (the first minute of a session, or a cutoff before every known session).
func requestWindow(sessions []session, cutoff time.Time) (start, end time.Time, ok bool) {
	if sessions == nil {
		return cutoff.UTC().Add(-noCalendarLookback), cutoff.UTC().Add(-barLength), true
	}
	w, found := referenceWindow(sessions, cutoff)
	if !found {
		return time.Time{}, time.Time{}, false
	}
	start, end = w.Open, w.End.Add(-barLength)
	return start, end, !end.Before(start)
}

// pickReference chooses the price of a symbol from its bars: the newest bar of the regular session
// whose end is not after the cutoff (see referenceWindow). Bars outside the session are never used,
// whatever Alpaca sent. The bar is stale when it ends more than five minutes before the time the
// newest bar would have ended. With no calendar (sessions is nil) it takes the newest finished bar of
// any hour, and says so: session unknown, stale.
func pickReference(bars []bar, sessions []session, cutoff time.Time) (reference, bool) {
	if sessions == nil {
		return pickNewest(bars, time.Time{}, cutoff.UTC(), cutoff.UTC(), domain.SessionUnknown, true)
	}
	w, found := referenceWindow(sessions, cutoff)
	if !found {
		return reference{}, false
	}
	return pickNewest(bars, w.Open, w.End, w.Expected, w.Status, false)
}

// pickNewest returns the newest bar that starts at or after open and ends at or before endBy.
func pickNewest(bars []bar, open, endBy, expected time.Time, status domain.SessionStatus, alwaysStale bool) (reference, bool) {
	var best *bar
	for i := range bars {
		b := &bars[i]
		if b.Start.Before(open) || b.Start.Add(barLength).After(endBy) {
			continue
		}
		if best == nil || b.Start.After(best.Start) {
			best = b
		}
	}
	if best == nil {
		return reference{}, false
	}
	asOf := best.Start.Add(barLength)
	return reference{Bar: *best, AsOf: asOf, Status: status, Stale: alwaysStale || expected.Sub(asOf) > staleAfter}, true
}
