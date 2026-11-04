package alpaca

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// barsServer answers GET /v2/stocks/bars from respond and records every query it received.
type barsServer struct {
	*fakeAlpaca
	mu      sync.Mutex
	queries []url.Values
}

func newBarsServer(t *testing.T, respond func(q url.Values) (int, string)) *barsServer {
	t.Helper()
	s := &barsServer{}
	s.fakeAlpaca = newFakeAlpaca(t, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.queries = append(s.queries, r.URL.Query())
		s.mu.Unlock()
		status, body := respond(r.URL.Query())
		answerJSON(status, body)(w, r)
	})
	return s
}

func (s *barsServer) query(i int) url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queries[i]
}

func TestReadBarsAsksForDelayedRawSIPMinuteBars(t *testing.T) {
	s := newBarsServer(t, func(url.Values) (int, string) { return 200, `{"bars":{},"next_page_token":null}` })
	start, end := utc(2026, 11, 3, 14, 30), utc(2026, 11, 3, 14, 44)

	if _, _, err := readBars(t.Context(), newTestClient(1), s.URL, []string{"AAPL", "MSFT"}, start, end); err != nil {
		t.Fatalf("readBars() error = %v", err)
	}

	q := s.query(0)
	want := map[string]string{
		"symbols": "AAPL,MSFT", "timeframe": "1Min", "feed": "sip", "adjustment": "raw", "sort": "desc", "limit": "10000",
		"start": "2026-11-03T14:30:00Z", "end": "2026-11-03T14:44:00Z",
	}
	for key, value := range want {
		if q.Get(key) != value {
			t.Errorf("query %s = %q, want %q", key, q.Get(key), value)
		}
	}
	if s.path.Load() != "/v2/stocks/bars" || s.keyID.Load() != testKeyID || s.secret.Load() != testSecret {
		t.Errorf("path = %v, key = %v, secret = %v", s.path.Load(), s.keyID.Load(), s.secret.Load())
	}
}

func TestReadBarsFollowsPagesUpToFive(t *testing.T) {
	t.Run("stops when every symbol has a bar", func(t *testing.T) {
		s := newBarsServer(t, func(q url.Values) (int, string) {
			if q.Get("page_token") == "" {
				return 200, `{"bars":{"AAPL":[{"t":"2026-11-03T14:44:00Z","c":1}]},"next_page_token":"p2"}`
			}
			return 200, `{"bars":{"MSFT":[{"t":"2026-11-03T14:44:00Z","c":2}]},"next_page_token":"p3"}`
		})
		got, _, err := readBars(t.Context(), newTestClient(1), s.URL, []string{"AAPL", "MSFT"}, utc(2026, 11, 3, 14, 30), utc(2026, 11, 3, 14, 44))
		if err != nil || len(got["AAPL"]) != 1 || len(got["MSFT"]) != 1 || len(s.queries) != 2 {
			t.Fatalf("got %+v, %v after %d requests; want both symbols after 2", got, err, len(s.queries))
		}
		if s.query(1).Get("page_token") != "p2" {
			t.Errorf("second request page_token = %q, want p2", s.query(1).Get("page_token"))
		}
	})
	t.Run("does not follow a token it does not need", func(t *testing.T) {
		s := newBarsServer(t, func(url.Values) (int, string) {
			return 200, `{"bars":{"AAPL":[{"t":"2026-11-03T14:44:00Z","c":1}]},"next_page_token":"more"}`
		})
		_, _, _ = readBars(t.Context(), newTestClient(1), s.URL, []string{"AAPL"}, utc(2026, 11, 3, 14, 30), utc(2026, 11, 3, 14, 44))
		if len(s.queries) != 1 {
			t.Errorf("%d requests, want 1: the only symbol already has a bar", len(s.queries))
		}
	})
	t.Run("gives up after five pages", func(t *testing.T) {
		s := newBarsServer(t, func(url.Values) (int, string) {
			return 200, `{"bars":{"AAPL":[{"t":"2026-11-03T14:44:00Z","c":1}]},"next_page_token":"again"}`
		})
		got, _, err := readBars(t.Context(), newTestClient(1), s.URL, []string{"AAPL", "MSFT"}, utc(2026, 11, 3, 14, 30), utc(2026, 11, 3, 14, 44))
		if err != nil || len(s.queries) != 5 || len(got["MSFT"]) != 0 {
			t.Errorf("got %+v, %v after %d requests; want 5 pages and nothing for MSFT", got, err, len(s.queries))
		}
	})
}

func TestReadBarsSkipsBarsItCannotRead(t *testing.T) {
	s := newBarsServer(t, func(url.Values) (int, string) {
		return 200, `{"bars":{"AAPL":[{"t":"yesterday","c":1},{"t":"2026-11-03T14:44:00Z"},{"t":"2026-11-03T14:43:00Z","c":3.25}]},"next_page_token":null}`
	})
	got, _, err := readBars(t.Context(), newTestClient(1), s.URL, []string{"AAPL"}, utc(2026, 11, 3, 14, 30), utc(2026, 11, 3, 14, 44))
	if err != nil || len(got["AAPL"]) != 1 || got["AAPL"][0].Close != "3.25" || !got["AAPL"][0].Start.Equal(utc(2026, 11, 3, 14, 43)) {
		t.Fatalf("got %+v, %v; want only the readable 14:43 bar with close 3.25", got, err)
	}
}

func TestReadBarsFailsOnBadAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"a 403":       {403, `{"message":"forbidden"}`},
		"a 429":       {429, ``},
		"not JSON":    {200, `<html>`},
		"a huge body": {200, `{"padding":"` + strings.Repeat("x", 9<<20) + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			s := newBarsServer(t, func(url.Values) (int, string) { return tc.status, tc.body })
			c := newAPIClient(testKeyID, testSecret, 5*time.Second, nil, 0)
			if _, _, err := readBars(t.Context(), c, s.URL, []string{"AAPL"}, utc(2026, 11, 3, 14, 30), utc(2026, 11, 3, 14, 44)); err == nil {
				t.Fatal("readBars() error = nil")
			}
		})
	}
}
