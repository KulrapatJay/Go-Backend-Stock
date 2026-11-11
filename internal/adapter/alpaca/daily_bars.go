package alpaca

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"go-backend-stock/internal/core/domain"
)

const (
	dailyBarsMaxBytes = 8 << 20
	// dailyBarsMaxPages bounds one read: a page holds up to 10,000 bars, which is about 40 symbols over the
	// longest range a client may ask for (domain.MaxBarRangeDays).
	dailyBarsMaxPages = 20
	// dailyBarsFeed is the feed of the bars. The free plan gets historical SIP bars that are at least 15
	// minutes old (AGENTS.md, ADR-0020).
	dailyBarsFeed = "sip"
)

// dailyBarsAnswer is the part of GET /v2/stocks/bars read for daily bars. Numbers stay json.Number so they
// never pass through a float.
type dailyBarsAnswer struct {
	Bars map[string][]struct {
		T string      `json:"t"`
		O json.Number `json:"o"`
		H json.Number `json:"h"`
		L json.Number `json:"l"`
		C json.Number `json:"c"`
		V json.Number `json:"v"`
	} `json:"bars"`
	NextPageToken *string `json:"next_page_token"`
}

// DailyBars returns the unadjusted daily bars of symbols dated from..to in New York, oldest first (see
// port.PriceProvider). It asks for delayed SIP bars with adjustment raw, newest end no later than now minus
// the reference delay (Alpaca answers 403 to anything newer on the free plan), and drops any bar dated
// today or later in New York, so a day that has not closed is never returned. A range with no closed day
// asks nothing. A symbol Alpaca has no bar for has an empty list.
//
// It returns an error, never a short list, when Alpaca cannot be asked, answers with a status but 200, sends
// something that is not bars, sends a bar whose numbers domain cannot hold exactly (an exponent, more than
// ten decimals, a zero price) or has more pages than the read limit: the caller records the range as
// fetched after a success, so a day skipped here would never be fetched again.
func (p *Prices) DailyBars(ctx context.Context, symbols []domain.Symbol, from, to domain.TradingDate) (domain.DailyBarBatch, error) {
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return domain.DailyBarBatch{}, errors.New("alpaca daily bars: range must run from an earlier date to a later one")
	}
	asked := uniqueSymbols(symbols)
	batch := domain.DailyBarBatch{
		Bars: make(map[string][]domain.DailyBar, len(asked)), Source: domain.BarSourceAlpaca, Feed: dailyBarsFeed,
	}
	for _, s := range asked {
		batch.Bars[s.String()] = []domain.DailyBar{}
	}

	now := p.now().UTC()
	lastClosed := domain.MarketDate(now).AddDays(-1)
	if to.After(lastClosed) {
		to = lastClosed
	}
	if len(asked) == 0 || to.Before(from) {
		return batch, nil
	}

	start := from.MarketDayStart()
	end := to.AddDays(1).MarketDayStart().Add(-time.Second)
	if limit := now.Add(-domain.ReferencePriceDelay); end.After(limit) {
		end = limit
	}
	names := make([]string, len(asked))
	for i, s := range asked {
		names[i] = s.String()
	}

	token := ""
	for page := 0; ; page++ {
		if page >= dailyBarsMaxPages {
			return domain.DailyBarBatch{}, errors.New("alpaca daily bars: more pages than the read limit")
		}
		q := url.Values{
			"symbols": {strings.Join(names, ",")}, "timeframe": {"1Day"}, "feed": {dailyBarsFeed}, "adjustment": {"raw"},
			"sort": {"asc"}, "limit": {"10000"},
			"start": {start.Format(time.RFC3339)}, "end": {end.Format(time.RFC3339)},
		}
		if token != "" {
			q.Set("page_token", token)
		}
		status, body, err := p.client.get(ctx, p.data+"/v2/stocks/bars?"+q.Encode(), dailyBarsMaxBytes)
		if err != nil {
			return domain.DailyBarBatch{}, fmt.Errorf("alpaca daily bars: %w", err)
		}
		if status != 200 {
			return domain.DailyBarBatch{}, statusError("alpaca daily bars", status)
		}
		var answer dailyBarsAnswer
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		// An answer without a "bars" object is not an answer: it must not read as "no bars" and be recorded as fetched.
		if err := dec.Decode(&answer); err != nil || answer.Bars == nil {
			return domain.DailyBarBatch{}, errors.New("alpaca daily bars: answer is not JSON bars")
		}
		for symbol, list := range answer.Bars {
			if _, wanted := batch.Bars[symbol]; !wanted {
				continue // Alpaca answered about a symbol that was not asked: ignore it
			}
			for _, raw := range list {
				bar, err := dailyBarFrom(raw.T, raw.O, raw.H, raw.L, raw.C, raw.V)
				if err != nil {
					return domain.DailyBarBatch{}, err
				}
				if bar.Date.Before(from) || bar.Date.After(to) {
					continue // not asked for, or not closed yet
				}
				batch.Bars[symbol] = append(batch.Bars[symbol], bar)
			}
		}
		if answer.NextPageToken == nil || *answer.NextPageToken == "" {
			break
		}
		token = *answer.NextPageToken
	}

	for symbol, list := range batch.Bars {
		batch.Bars[symbol] = oneBarPerDay(list)
	}
	return batch, nil
}

// dailyBarFrom reads one bar. Its errors never repeat the values Alpaca sent.
func dailyBarFrom(t string, o, h, l, c, v json.Number) (domain.DailyBar, error) {
	stamp, err := time.Parse(time.RFC3339Nano, t)
	if err != nil {
		return domain.DailyBar{}, errors.New("alpaca daily bars: a bar has no readable time")
	}
	var bar domain.DailyBar
	bar.Date = domain.MarketDate(stamp)
	for _, f := range []struct {
		dst *domain.Price
		src json.Number
	}{{&bar.Open, o}, {&bar.High, h}, {&bar.Low, l}, {&bar.Close, c}} {
		if *f.dst, err = domain.ParsePrice(f.src.String()); err != nil {
			return domain.DailyBar{}, errors.New("alpaca daily bars: a bar has a price that cannot be stored exactly")
		}
	}
	if bar.Volume, err = domain.ParseVolume(v.String()); err != nil {
		return domain.DailyBar{}, errors.New("alpaca daily bars: a bar has a volume that cannot be stored exactly")
	}
	if bar, err = domain.NewDailyBar(bar); err != nil {
		return domain.DailyBar{}, errors.New("alpaca daily bars: a bar is not a valid bar")
	}
	return bar, nil
}

// oneBarPerDay sorts bars oldest first and keeps the first bar of each date.
func oneBarPerDay(bars []domain.DailyBar) []domain.DailyBar {
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].Date.Before(bars[j].Date) })
	out := bars[:0]
	for _, b := range bars {
		if len(out) > 0 && out[len(out)-1].Date.Equal(b.Date) {
			continue
		}
		out = append(out, b)
	}
	return out
}
