package service

import (
	"context"
	"fmt"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// barFillChunk is how many symbols one provider request asks about.
const barFillChunk = 10

// barFiller asks the price provider for the daily bars a stored copy lacks and stores them (ADR-0024). It
// holds no transaction and no lock while the provider answers, and sets no timeout of its own: the caller
// passes a context that has one.
type barFiller struct {
	store  port.Repositories
	prices port.PriceProvider
	source domain.BarSource
	clock  port.Clock
}

// fetchRange asks the provider for r of all instruments in one request and saves each instrument's bars
// with r as the fetched range. providerErr: the provider failed. err: the answer cannot be trusted
// (another source, a bar outside r) or could not be stored. Nothing is saved from an answer that cannot be
// trusted.
func (f barFiller) fetchRange(ctx context.Context, instruments []domain.Instrument, r domain.DateRange) (providerErr, err error) {
	symbols := make([]domain.Symbol, len(instruments))
	for i, in := range instruments {
		symbols[i] = in.Symbol
	}
	batch, askErr := f.prices.DailyBars(ctx, symbols, r.From, r.To)
	if askErr != nil {
		return askErr, nil
	}
	if batch.Source != f.source {
		return nil, fmt.Errorf("%w: the price provider answered as %q but the service is set to %q", domain.ErrInternal, batch.Source, f.source)
	}
	for _, in := range instruments {
		for _, b := range batch.Bars[in.Symbol.String()] {
			if b.Date.Before(r.From) || b.Date.After(r.To) {
				return nil, fmt.Errorf("%w: the price provider sent a bar outside the range asked", domain.ErrInternal)
			}
		}
	}
	now := NowForStorage(f.clock)
	for _, in := range instruments {
		if err := f.store.PriceBars().Save(ctx, in.ID, f.source, batch.Bars[in.Symbol.String()], r, now); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// fill makes the coverage of every instrument include r where the provider allows: it reads the
// coverages, groups the instruments by the range each is missing (domain.MissingRanges), and calls
// fetchRange for at most barFillChunk instruments at a time. It stops at the first providerErr or err;
// what was saved before stays.
func (f barFiller) fill(ctx context.Context, instruments []domain.Instrument, r domain.DateRange) (providerErr, err error) {
	ids := make([]domain.ID, len(instruments))
	for i, in := range instruments {
		ids[i] = in.ID
	}
	covered, err := f.store.PriceBars().Coverages(ctx, ids, f.source)
	if err != nil {
		return nil, err
	}

	// Groups keep the order in which a missing range first appears, so the requests are made in a stable order.
	var order []string
	ranges := map[string]domain.DateRange{}
	groups := map[string][]domain.Instrument{}
	for _, in := range instruments {
		var known *domain.BarCoverage
		if c, ok := covered[in.ID]; ok {
			known = &c
		}
		for _, missing := range domain.MissingRanges(known, r.From, r.To) {
			key := rangeString(missing)
			if _, seen := groups[key]; !seen {
				order = append(order, key)
				ranges[key] = missing
			}
			groups[key] = append(groups[key], in)
		}
	}
	for _, key := range order {
		members := groups[key]
		for len(members) > 0 {
			n := min(barFillChunk, len(members))
			if providerErr, err = f.fetchRange(ctx, members[:n], ranges[key]); providerErr != nil || err != nil {
				return providerErr, err
			}
			members = members[n:]
		}
	}
	return nil, nil
}

func rangeString(r domain.DateRange) string { return r.From.String() + ".." + r.To.String() }
