package alpaca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	calendarMaxBytes = 256 << 10
	calendarTTL      = 6 * time.Hour
	calendarDays     = 10               // how far back sessions are asked for
	calendarTimeout  = 2 * time.Second  // the calendar is asked once, and for no longer than this
	calendarPause    = 30 * time.Second // after a failed read the calendar is not asked again for this long
)

// session is one regular trading session of the market, in UTC.
type session struct {
	Date        string // the market's calendar date, YYYY-MM-DD
	Open, Close time.Time
}

// calendar reads the NYSE trading calendar from the Trading API.
type calendar struct {
	client  *apiClient
	baseURL string
	now     func() time.Time

	mu         sync.Mutex
	entries    map[string]calendarEntry // keyed by the UTC date of the cutoff
	failedAt   time.Time
	failureErr error
}

type calendarEntry struct {
	sessions []session
	at       time.Time
}

func newCalendar(c *apiClient, baseURL string, now func() time.Time) *calendar {
	return &calendar{client: c, baseURL: baseURL, now: now, entries: map[string]calendarEntry{}}
}

type calendarAnswer struct {
	Calendar []struct {
		Date      string `json:"date"`
		CoreStart string `json:"core_start"`
		CoreEnd   string `json:"core_end"`
	} `json:"calendar"`
}

// sessions returns the regular sessions of the days up to cutoff, oldest first. Holidays and weekends
// are simply not in the list. The answer is kept for six hours per cutoff date. When a read fails, an
// entry past that time is still used (the calendar of a date does not change), and the failure is
// remembered for thirty seconds so that a calendar that is down does not take time from every request.
func (k *calendar) sessions(ctx context.Context, cutoff time.Time) ([]session, error) {
	day := cutoff.UTC().Format(time.DateOnly)
	k.mu.Lock()
	entry, ok := k.entries[day]
	paused := k.failureErr != nil && k.now().Sub(k.failedAt) < calendarPause
	failure := k.failureErr
	k.mu.Unlock()
	if ok && k.now().Sub(entry.at) < calendarTTL {
		return entry.sessions, nil
	}
	if paused {
		if ok {
			return entry.sessions, nil
		}
		return nil, failure
	}
	sessions, err := k.read(ctx, cutoff, day)
	if err != nil {
		if ctx.Err() == nil {
			k.mu.Lock()
			k.failedAt, k.failureErr = k.now(), err
			k.mu.Unlock()
		}
		if ok {
			return entry.sessions, nil
		}
		return nil, err
	}
	k.mu.Lock()
	k.entries[day] = calendarEntry{sessions: sessions, at: k.now()}
	k.failureErr = nil
	k.mu.Unlock()
	return sessions, nil
}

func (k *calendar) read(ctx context.Context, cutoff time.Time, day string) ([]session, error) {

	start := cutoff.UTC().AddDate(0, 0, -calendarDays).Format(time.DateOnly)
	rawURL := fmt.Sprintf("%s/v3/calendar/XNYS?start=%s&end=%s&timezone=UTC", k.baseURL, start, day)
	status, body, err := k.client.get(ctx, rawURL, calendarMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("alpaca calendar: %w", err)
	}
	if status != 200 {
		return nil, statusError("alpaca calendar", status)
	}
	return parseCalendar(body)
}

func parseCalendar(body []byte) ([]session, error) {
	var answer calendarAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, errors.New("alpaca calendar: answer is not a JSON calendar")
	}
	if len(answer.Calendar) == 0 {
		return nil, errors.New("alpaca calendar: answer has no trading days")
	}
	sessions := make([]session, 0, len(answer.Calendar))
	for _, item := range answer.Calendar {
		open, openErr := time.Parse(time.RFC3339, item.CoreStart)
		closeAt, closeErr := time.Parse(time.RFC3339, item.CoreEnd)
		if item.Date == "" || openErr != nil || closeErr != nil || !closeAt.After(open) {
			return nil, errors.New("alpaca calendar: answer has a trading day that cannot be read")
		}
		sessions = append(sessions, session{Date: item.Date, Open: open.UTC(), Close: closeAt.UTC()})
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Open.Before(sessions[j].Open) })
	return sessions, nil
}
