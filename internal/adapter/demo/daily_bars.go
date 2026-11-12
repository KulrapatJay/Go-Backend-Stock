package demo

import (
	"context"
	"errors"
	"hash/fnv"
	"time"

	"github.com/shopspring/decimal"

	"go-backend-stock/internal/core/domain"
)

// demoHistoryStart is the first day with a demo bar: where the history of the free Alpaca plan starts, so
// the demo shows a chart as long as the real one.
var demoHistoryStart = domain.NewTradingDate(2016, time.January, 4)

const (
	// wavePeriod is how many days one up-and-down swing of a demo price takes.
	wavePeriod = 120
	// waveStepBP is how many basis points one day of the swing moves the price: the price swings between
	// 12% below and 12% above the demo price.
	waveStepBP = 40
)

// DailyBars returns invented daily bars for the demo list: weekdays only, from 2016-01-04 to the last
// closed day, oldest first. The bar of a symbol and a date is a fixed function of the two, never of the
// range asked, so two fetches of different ranges stored side by side agree and a chart looks the same
// every time. The prices are integers of basis points around the demo price, rounded to cents; there is no
// float. They are sample data, not market data, and the batch says so. A symbol outside the demo list has
// an empty list.
func (p *Prices) DailyBars(ctx context.Context, symbols []domain.Symbol, from, to domain.TradingDate) (domain.DailyBarBatch, error) {
	if err := ctx.Err(); err != nil {
		return domain.DailyBarBatch{}, err
	}
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return domain.DailyBarBatch{}, errors.New("demo daily bars: range must run from an earlier date to a later one")
	}
	batch := domain.DailyBarBatch{
		Bars: make(map[string][]domain.DailyBar, len(symbols)), Source: domain.BarSourceDemo, Feed: "demo",
	}
	if from.Before(demoHistoryStart) {
		from = demoHistoryStart
	}
	if last := domain.MarketDate(p.clock.Now()).AddDays(-1); to.After(last) {
		to = last
	}
	for _, symbol := range symbols {
		name := symbol.String()
		batch.Bars[name] = []domain.DailyBar{}
		raw, ok := demoPrices[name]
		if !ok {
			continue
		}
		base, err := decimal.NewFromString(raw)
		if err != nil {
			return domain.DailyBarBatch{}, err
		}
		for day := from; !day.After(to); day = day.AddDays(1) {
			if wd := day.UTC().Weekday(); wd == time.Saturday || wd == time.Sunday {
				continue
			}
			bar, err := demoBar(name, base, day)
			if err != nil {
				return domain.DailyBarBatch{}, err
			}
			batch.Bars[name] = append(batch.Bars[name], bar)
		}
	}
	return batch, nil
}

// demoBar makes the bar of one symbol on one day from a hash of the two and a slow swing of the price.
func demoBar(symbol string, base decimal.Decimal, day domain.TradingDate) (domain.DailyBar, error) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(symbol + "|" + day.String()))
	sum := h.Sum64()

	n := demoHistoryStart.DaysUntil(day)
	step := n % wavePeriod
	if step > wavePeriod/2 {
		step = wavePeriod - step
	}
	wave := (step - wavePeriod/4) * waveStepBP // basis points, between -1200 and +1200

	closeBP := 10000 + wave + int(sum%301) - 150
	openBP := 10000 + wave + int((sum>>16)%301) - 150
	closePrice, openPrice := atBP(base, closeBP), atBP(base, openBP)
	highBP, lowBP := 10000+int((sum>>32)%101), 10000-int((sum>>40)%101)
	highPrice, lowPrice := atBP(decimal.Max(openPrice, closePrice), highBP), atBP(decimal.Min(openPrice, closePrice), lowBP)

	var bar domain.DailyBar
	bar.Date = day
	for _, f := range []struct {
		dst *domain.Price
		src decimal.Decimal
	}{{&bar.Open, openPrice}, {&bar.High, highPrice}, {&bar.Low, lowPrice}, {&bar.Close, closePrice}} {
		var err error
		if *f.dst, err = domain.ParsePrice(f.src.String()); err != nil {
			return domain.DailyBar{}, err
		}
	}
	volume, err := domain.ParseVolume(decimal.NewFromInt(1_000_000 + int64((sum>>8)%9_000_000)).String())
	if err != nil {
		return domain.DailyBar{}, err
	}
	bar.Volume = volume
	return domain.NewDailyBar(bar)
}

// atBP returns base times bp/10000 rounded to cents, without a division.
func atBP(base decimal.Decimal, bp int) decimal.Decimal {
	return base.Mul(decimal.NewFromInt(int64(bp))).Shift(-4).Round(2)
}
