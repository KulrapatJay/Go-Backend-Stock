package alpaca

import (
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

const novemberCalendar = `{"market":{"acronym":"NYSE","name":"New York Stock Exchange","timezone":"America/New_York"},"calendar":[
 {"date":"2026-11-27","core_start":"2026-11-27T09:30:00-05:00","core_end":"2026-11-27T13:00:00-05:00"},
 {"date":"2026-11-03","core_start":"2026-11-03T09:30:00-05:00","core_end":"2026-11-03T16:00:00-05:00"}]}`

func newTestCalendar(t *testing.T, f *fakeAlpaca) (*calendar, *clock) {
	t.Helper()
	clk := &clock{now: time.Date(2026, time.November, 27, 19, 0, 0, 0, time.UTC)}
	return newCalendar(newTestClient(1), f.URL, clk.Now), clk
}

func utc(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
}

func TestCalendarReadsRegularSessions(t *testing.T) {
	var query atomic.Value
	f := newFakeAlpaca(t, func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.Query())
		answerJSON(200, novemberCalendar)(w, r)
	})
	k, clk := newTestCalendar(t, f)

	got, err := k.sessions(t.Context(), clk.now)
	if err != nil {
		t.Fatalf("sessions() error = %v", err)
	}

	if f.path.Load() != "/v3/calendar/XNYS" {
		t.Errorf("path = %v, want /v3/calendar/XNYS", f.path.Load())
	}
	q := query.Load().(url.Values)
	if q.Get("start") != "2026-11-17" || q.Get("end") != "2026-11-27" || q.Get("timezone") != "UTC" {
		t.Errorf("query = %v, want start=2026-11-17 end=2026-11-27 timezone=UTC", q)
	}
	want := []session{
		{Date: "2026-11-03", Open: utc(2026, 11, 3, 14, 30), Close: utc(2026, 11, 3, 21, 0)},
		{Date: "2026-11-27", Open: utc(2026, 11, 27, 14, 30), Close: utc(2026, 11, 27, 18, 0)}, // the early close
	}
	if len(got) != 2 {
		t.Fatalf("sessions = %+v, want 2", got)
	}
	for i := range want {
		if got[i].Date != want[i].Date || !got[i].Open.Equal(want[i].Open) || !got[i].Close.Equal(want[i].Close) ||
			got[i].Open.Location() != time.UTC || got[i].Close.Location() != time.UTC {
			t.Errorf("session %d = %+v, want %+v in UTC, oldest first", i, got[i], want[i])
		}
	}
}

func TestCalendarIsCachedForSixHours(t *testing.T) {
	f := newFakeAlpaca(t, answerJSON(200, novemberCalendar))
	k, clk := newTestCalendar(t, f)
	cutoff := clk.now // the clock moves, the cutoff of the question does not

	for _, advance := range []time.Duration{0, 5 * time.Hour} {
		clk.advance(advance)
		if _, err := k.sessions(t.Context(), cutoff); err != nil {
			t.Fatalf("sessions() error = %v", err)
		}
	}
	if got := f.hits.Load(); got != 1 {
		t.Fatalf("Alpaca was asked %d times within 5 hours, want once", got)
	}
	clk.advance(time.Hour - time.Nanosecond)
	_, _ = k.sessions(t.Context(), cutoff)
	if got := f.hits.Load(); got != 1 {
		t.Fatalf("asked %d times just before 6 hours, want once", got)
	}
	clk.advance(time.Nanosecond)
	_, _ = k.sessions(t.Context(), cutoff)
	if got := f.hits.Load(); got != 2 {
		t.Fatalf("asked %d times at 6 hours, want a fresh question", got)
	}
}

func TestCalendarKeepsADayPerCutoffDate(t *testing.T) {
	f := newFakeAlpaca(t, answerJSON(200, novemberCalendar))
	k, clk := newTestCalendar(t, f)
	for _, cutoff := range []time.Time{clk.now, clk.now.Add(time.Hour), clk.now.AddDate(0, 0, 1)} {
		if _, err := k.sessions(t.Context(), cutoff); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.hits.Load(); got != 2 {
		t.Errorf("Alpaca was asked %d times for two cutoff dates, want 2", got)
	}
}

func TestCalendarErrorsAreNotCached(t *testing.T) {
	f := newFakeAlpaca(t, sequence(status(500), status(500), ok(novemberCalendar)))
	k, clk := newTestCalendar(t, f)

	if _, err := k.sessions(t.Context(), clk.now); err == nil {
		t.Fatal("sessions() error = nil after two 500s, want an error")
	}
	if _, err := k.sessions(t.Context(), clk.now); err != nil {
		t.Fatalf("second sessions() error = %v, want the calendar once Alpaca answers", err)
	}
}

func TestCalendarRefusesMalformedAnswers(t *testing.T) {
	bodies := map[string]string{
		"not JSON":                             `<html>`,
		"no calendar":                          `{"market":{}}`,
		"an empty calendar":                    `{"calendar":[]}`,
		"a bad core_start":                     `{"calendar":[{"date":"2026-11-03","core_start":"yesterday","core_end":"2026-11-03T16:00:00-05:00"}]}`,
		"a bad core_end":                       `{"calendar":[{"date":"2026-11-03","core_start":"2026-11-03T09:30:00-05:00","core_end":""}]}`,
		"a session that ends before it starts": `{"calendar":[{"date":"2026-11-03","core_start":"2026-11-03T16:00:00-05:00","core_end":"2026-11-03T09:30:00-05:00"}]}`,
		"a missing date":                       `{"calendar":[{"core_start":"2026-11-03T09:30:00-05:00","core_end":"2026-11-03T16:00:00-05:00"}]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			f := newFakeAlpaca(t, answerJSON(200, body))
			k, clk := newTestCalendar(t, f)
			if got, err := k.sessions(t.Context(), clk.now); err == nil {
				t.Fatalf("sessions() = %+v, want an error", got)
			}
		})
	}
}

func TestCalendarFailsOnBadStatus(t *testing.T) {
	for _, code := range []int{401, 403, 404} {
		f := newFakeAlpaca(t, status(code))
		k, clk := newTestCalendar(t, f)
		if _, err := k.sessions(t.Context(), clk.now); err == nil {
			t.Errorf("status %d: sessions() error = nil", code)
		}
	}
}
