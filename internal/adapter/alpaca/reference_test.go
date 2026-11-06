package alpaca

import (
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// Sessions are abstract UTC sessions of 14:30-21:00 so the expectations read as plain clock times.
func regularSession(year int, month time.Month, day int) session {
	return session{
		Date: time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Format(time.DateOnly),
		Open: utc(year, month, day, 14, 30), Close: utc(year, month, day, 21, 0),
	}
}

func barAt(year int, month time.Month, day, hour, minute int, closePrice string) bar {
	return bar{Start: utc(year, month, day, hour, minute), Close: closePrice}
}

func at(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
}

func TestReferenceUsesTheEndOfTheBar(t *testing.T) {
	sessions := []session{regularSession(2026, 11, 3)}
	bars := []bar{barAt(2026, 11, 3, 14, 43, "1"), barAt(2026, 11, 3, 14, 44, "2"), barAt(2026, 11, 3, 14, 45, "3")}

	got, ok := pickReference(bars, sessions, at(2026, 11, 3, 14, 45, 30))

	if !ok || got.Bar.Close != "2" {
		t.Fatalf("pickReference() = %+v, %v; want the 14:44 bar: the 14:45 bar has not ended at 14:45", got, ok)
	}
	if !got.AsOf.Equal(utc(2026, 11, 3, 14, 45)) || got.Status != domain.SessionOpen || got.Stale {
		t.Errorf("reference = %+v, want as_of 14:45 (the end of the bar), open, not stale", got)
	}
	if _, ok := pickReference([]bar{barAt(2026, 11, 3, 14, 45, "3")}, sessions, at(2026, 11, 3, 14, 45, 30)); ok {
		t.Error("a bar that starts at the cutoff minute was used before it ended")
	}
}

func TestReferenceIsStaleAfterFiveMissingMinutes(t *testing.T) {
	sessions := []session{regularSession(2026, 11, 3)}
	cutoff := at(2026, 11, 3, 15, 0, 10)

	fresh, ok := pickReference([]bar{barAt(2026, 11, 3, 14, 54, "1")}, sessions, cutoff) // ends 14:55, 5 minutes behind
	if !ok || fresh.Stale {
		t.Errorf("5 minutes behind: %+v, %v; want not stale", fresh, ok)
	}
	stale, ok := pickReference([]bar{barAt(2026, 11, 3, 14, 53, "1")}, sessions, cutoff) // ends 14:54, 6 minutes behind
	if !ok || !stale.Stale || stale.Status != domain.SessionOpen {
		t.Errorf("6 minutes behind: %+v, %v; want stale while the session is open", stale, ok)
	}
}

func TestReferenceAcrossHolidayWeekendAndEarlyClose(t *testing.T) {
	friday := regularSession(2026, 10, 30)
	monday, tuesday := regularSession(2026, 11, 2), regularSession(2026, 11, 3)
	wednesday := regularSession(2026, 11, 25)
	earlyClose := session{Date: "2026-11-27", Open: utc(2026, 11, 27, 14, 30), Close: utc(2026, 11, 27, 18, 0)}

	t.Run("a Saturday uses Friday's closing bar", func(t *testing.T) {
		got, ok := pickReference([]bar{barAt(2026, 10, 30, 20, 59, "9")}, []session{friday}, at(2026, 10, 31, 12, 0, 0))
		if !ok || got.Bar.Close != "9" || !got.AsOf.Equal(utc(2026, 10, 30, 21, 0)) || got.Status != domain.SessionMarketClosed || got.Stale {
			t.Errorf("reference = %+v, %v; want Friday 21:00, market_closed, not stale", got, ok)
		}
	})
	t.Run("before the open uses the previous session", func(t *testing.T) {
		got, ok := pickReference([]bar{barAt(2026, 11, 2, 20, 59, "8")}, []session{monday, tuesday}, at(2026, 11, 3, 13, 0, 0))
		if !ok || got.Bar.Close != "8" || got.Status != domain.SessionMarketClosed || got.Stale {
			t.Errorf("reference = %+v, %v; want Monday's close, market_closed", got, ok)
		}
	})
	t.Run("an early close is closed at its own close", func(t *testing.T) {
		got, ok := pickReference([]bar{barAt(2026, 11, 27, 17, 59, "7")}, []session{earlyClose}, at(2026, 11, 27, 19, 0, 0))
		if !ok || !got.AsOf.Equal(utc(2026, 11, 27, 18, 0)) || got.Status != domain.SessionMarketClosed || got.Stale {
			t.Errorf("reference = %+v, %v; want 18:00 market_closed, not stale", got, ok)
		}
		quiet, ok := pickReference([]bar{barAt(2026, 11, 27, 17, 50, "7")}, []session{earlyClose}, at(2026, 11, 27, 19, 0, 0))
		if !ok || !quiet.Stale || quiet.Status != domain.SessionMarketClosed {
			t.Errorf("a last bar 9 minutes before the close: %+v, %v; want market_closed and stale", quiet, ok)
		}
	})
	t.Run("a holiday uses the session before it", func(t *testing.T) {
		got, ok := pickReference([]bar{barAt(2026, 11, 25, 20, 59, "6")}, []session{wednesday, earlyClose}, at(2026, 11, 26, 15, 0, 0))
		if !ok || got.Bar.Close != "6" || got.Status != domain.SessionMarketClosed {
			t.Errorf("reference = %+v, %v; want Wednesday's close on Thanksgiving", got, ok)
		}
	})
}

func TestReferenceIgnoresBarsOutsideTheRegularSession(t *testing.T) {
	sessions := []session{regularSession(2026, 11, 3)}
	pre, inside := barAt(2026, 11, 3, 14, 29, "pre"), barAt(2026, 11, 3, 14, 40, "in")

	if got, ok := pickReference([]bar{pre, inside}, sessions, at(2026, 11, 3, 15, 0, 10)); !ok || got.Bar.Close != "in" {
		t.Errorf("reference = %+v, %v; want the in-session bar, not the 14:29 pre-market bar", got, ok)
	}
	if got, ok := pickReference([]bar{pre}, sessions, at(2026, 11, 3, 15, 0, 10)); ok {
		t.Errorf("reference = %+v; a pre-market bar alone must not be a price", got)
	}
	after := barAt(2026, 10, 30, 21, 5, "after")
	if got, ok := pickReference([]bar{after}, []session{regularSession(2026, 10, 30)}, at(2026, 10, 31, 12, 0, 0)); ok {
		t.Errorf("reference = %+v; an after-hours bar alone must not be a price", got)
	}
}

func TestReferenceWithoutACalendarIsUnknownAndStale(t *testing.T) {
	got, ok := pickReference([]bar{barAt(2026, 11, 3, 14, 58, "5"), barAt(2026, 11, 3, 14, 40, "4")}, nil, at(2026, 11, 3, 15, 0, 10))
	if !ok || got.Bar.Close != "5" || got.Status != domain.SessionUnknown || !got.Stale || !got.AsOf.Equal(utc(2026, 11, 3, 14, 59)) {
		t.Errorf("reference = %+v, %v; want the latest finished bar, unknown and stale", got, ok)
	}
	if _, ok := pickReference([]bar{barAt(2026, 11, 3, 15, 0, "late")}, nil, at(2026, 11, 3, 15, 0, 10)); ok {
		t.Error("a bar that had not ended at the cutoff was used without a calendar")
	}
}

func TestReferenceTakesTheLatestBarWhateverTheOrder(t *testing.T) {
	sessions := []session{regularSession(2026, 11, 3)}
	bars := []bar{barAt(2026, 11, 3, 14, 40, "old"), barAt(2026, 11, 3, 14, 44, "new"), barAt(2026, 11, 3, 14, 42, "mid")}
	if got, ok := pickReference(bars, sessions, at(2026, 11, 3, 14, 45, 30)); !ok || got.Bar.Close != "new" {
		t.Errorf("reference = %+v, %v; want the 14:44 bar", got, ok)
	}
}

func TestRequestWindow(t *testing.T) {
	sessions := []session{regularSession(2026, 11, 2), regularSession(2026, 11, 3)}

	start, end, ok := requestWindow(sessions, at(2026, 11, 3, 14, 45, 30))
	if !ok || !start.Equal(utc(2026, 11, 3, 14, 30)) || !end.Equal(utc(2026, 11, 3, 14, 44)) {
		t.Errorf("open session: [%v, %v] %v, want [14:30, 14:44]", start, end, ok)
	}
	start, end, ok = requestWindow(sessions, at(2026, 11, 3, 22, 0, 0))
	if !ok || !start.Equal(utc(2026, 11, 3, 14, 30)) || !end.Equal(utc(2026, 11, 3, 20, 59)) {
		t.Errorf("after the close: [%v, %v] %v, want today's whole session ending 20:59", start, end, ok)
	}
	start, end, ok = requestWindow(nil, at(2026, 11, 3, 15, 0, 10))
	if !ok || !start.Equal(at(2026, 10, 30, 15, 0, 10)) || !end.Equal(at(2026, 11, 3, 14, 59, 10)) {
		t.Errorf("no calendar: [%v, %v] %v, want [cutoff-96h, cutoff-1m]", start, end, ok)
	}
	if _, _, ok := requestWindow(sessions, at(2026, 11, 3, 14, 30, 20)); ok {
		t.Error("the first minute of a session has no finished bar yet, but the window was not empty")
	}
	if _, _, ok := requestWindow([]session{regularSession(2026, 11, 3)}, at(2026, 11, 2, 12, 0, 0)); ok {
		t.Error("a cutoff before every known session got a window")
	}
}
