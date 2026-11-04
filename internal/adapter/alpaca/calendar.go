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
	calendarDays     = 10 // how far back sessions are asked for
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

	mu      sync.Mutex
	entries map[string]calendarEntry // keyed by the UTC date of the cutoff
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
// are simply not in the list. The answer is kept for six hours per cutoff date; an error is not.
func (k *calendar) sessions(ctx context.Context, cutoff time.Time) ([]session, error) {
	day := cutoff.UTC().Format(time.DateOnly)
	k.mu.Lock()
	entry, ok := k.entries[day]
	k.mu.Unlock()
	if ok && k.now().Sub(entry.at) < calendarTTL {
		return entry.sessions, nil
	}

	start := cutoff.UTC().AddDate(0, 0, -calendarDays).Format(time.DateOnly)
	rawURL := fmt.Sprintf("%s/v3/calendar/XNYS?start=%s&end=%s&timezone=UTC", k.baseURL, start, day)
	status, body, err := k.client.get(ctx, rawURL, calendarMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("alpaca calendar: %w", err)
	}
	if status != 200 {
		return nil, statusError("alpaca calendar", status)
	}
	sessions, err := parseCalendar(body)
	if err != nil {
		return nil, err
	}
	k.mu.Lock()
	k.entries[day] = calendarEntry{sessions: sessions, at: k.now()}
	k.mu.Unlock()
	return sessions, nil
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
