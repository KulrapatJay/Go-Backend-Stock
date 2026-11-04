package alpaca

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	barsMaxBytes = 8 << 20
	barsMaxPages = 5
)

// barsAnswer is the part of GET /v2/stocks/bars that is read. Prices stay json.Number so they never
// pass through a float.
type barsAnswer struct {
	Bars map[string][]struct {
		T string      `json:"t"`
		C json.Number `json:"c"`
	} `json:"bars"`
	NextPageToken *string `json:"next_page_token"`
}

// readBars asks the Market Data API for the minute bars of symbols whose start lies in [start, end]:
// delayed SIP, unadjusted, newest first. It follows next_page_token for at most five pages and stops as
// soon as every symbol has a bar. A bar whose time or close cannot be read is skipped. Any HTTP status
// but 200 is an error that names the status and nothing Alpaca said. unread is true when the page limit
// ended the read with more pages to come and some symbol without a bar: Alpaca was never heard on it.
func readBars(ctx context.Context, c *apiClient, baseURL string, symbols []string, start, end time.Time) (bars map[string][]bar, unread bool, err error) {
	out := make(map[string][]bar, len(symbols))
	token := ""
	for page := 0; page < barsMaxPages; page++ {
		q := url.Values{
			"symbols": {strings.Join(symbols, ",")}, "timeframe": {"1Min"}, "feed": {"sip"}, "adjustment": {"raw"},
			"sort": {"desc"}, "limit": {"10000"},
			"start": {start.UTC().Format(time.RFC3339)}, "end": {end.UTC().Format(time.RFC3339)},
		}
		if token != "" {
			q.Set("page_token", token)
		}
		status, body, err := c.get(ctx, baseURL+"/v2/stocks/bars?"+q.Encode(), barsMaxBytes)
		if err != nil {
			return nil, false, fmt.Errorf("alpaca bars: %w", err)
		}
		if status != 200 {
			return nil, false, statusError("alpaca bars", status)
		}
		var answer barsAnswer
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&answer); err != nil {
			return nil, false, errors.New("alpaca bars: answer is not JSON bars")
		}
		for symbol, list := range answer.Bars {
			for _, b := range list {
				t, err := time.Parse(time.RFC3339Nano, b.T)
				if err != nil || b.C.String() == "" {
					continue
				}
				out[symbol] = append(out[symbol], bar{Start: t.UTC(), Close: b.C.String()})
			}
		}
		if everySymbolHasABar(out, symbols) || answer.NextPageToken == nil || *answer.NextPageToken == "" {
			return out, false, nil
		}
		token = *answer.NextPageToken
	}
	return out, true, nil
}

func everySymbolHasABar(bars map[string][]bar, symbols []string) bool {
	for _, s := range symbols {
		if len(bars[s]) == 0 {
			return false
		}
	}
	return true
}
