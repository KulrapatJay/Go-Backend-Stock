package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PriceBarRepository stores the daily bars of stocks (ADR-0024). Reads use the pool; Save runs its own
// transaction so the bars and the coverage that records them commit together.
type PriceBarRepository struct {
	store *Store
}

var _ port.PriceBarRepository = (*PriceBarRepository)(nil)

// Coverage returns the range of dates already asked for the instrument and source.
func (r *PriceBarRepository) Coverage(ctx context.Context, instrumentID domain.ID, source domain.BarSource) (domain.BarCoverage, bool, error) {
	if err := checkBarKey(instrumentID, source); err != nil {
		return domain.BarCoverage{}, false, err
	}
	row, err := sqlc.New(r.store.pool).GetPriceBarCoverage(ctx, sqlc.GetPriceBarCoverageParams{
		InstrumentID: pgUUID(instrumentID), Source: string(source),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BarCoverage{}, false, nil
	}
	if err != nil {
		return domain.BarCoverage{}, false, mapError(err)
	}
	return domain.BarCoverage{From: tradingDateFromPG(row.CoveredFrom), Through: tradingDateFromPG(row.CoveredThrough)}, true, nil
}

// Range returns the stored bars with from <= date <= to, oldest first.
func (r *PriceBarRepository) Range(ctx context.Context, instrumentID domain.ID, source domain.BarSource, from, to domain.TradingDate) ([]domain.DailyBar, error) {
	if err := checkBarKey(instrumentID, source); err != nil {
		return nil, err
	}
	rows, err := sqlc.New(r.store.pool).ListPriceBars(ctx, sqlc.ListPriceBarsParams{
		InstrumentID: pgUUID(instrumentID), Source: string(source), FromDate: pgDate(from), ToDate: pgDate(to),
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]domain.DailyBar, 0, len(rows))
	for _, row := range rows {
		bar, err := dailyBarFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, bar)
	}
	return out, nil
}

// Save stores the bars and widens the coverage in one transaction. It checks everything it can before it
// opens the transaction, so a bar outside the fetched range writes nothing.
func (r *PriceBarRepository) Save(ctx context.Context, instrumentID domain.ID, source domain.BarSource, bars []domain.DailyBar, fetched domain.DateRange, now time.Time) error {
	if err := checkBarKey(instrumentID, source); err != nil {
		return err
	}
	if fetched.From.IsZero() || fetched.To.IsZero() || fetched.To.Before(fetched.From) {
		return fmt.Errorf("%w: fetched range must run from an earlier date to a later one", domain.ErrInternal)
	}
	rows := make([]sqlc.InsertPriceBarParams, 0, len(bars))
	for _, b := range bars {
		if b.Date.IsZero() || b.Date.Before(fetched.From) || b.Date.After(fetched.To) {
			return fmt.Errorf("%w: a bar is dated outside the range it was fetched for", domain.ErrInternal)
		}
		row, err := priceBarParams(instrumentID, source, b, now)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}

	return r.store.withinTx(ctx, func(tx *txRepositories) error {
		if len(rows) > 0 {
			var firstErr error
			tx.q.InsertPriceBar(ctx, rows).Exec(func(_ int, err error) {
				if err != nil && firstErr == nil {
					firstErr = err
				}
			})
			if firstErr != nil {
				return mapError(firstErr)
			}
		}
		return mapError(tx.q.UpsertPriceBarCoverage(ctx, sqlc.UpsertPriceBarCoverageParams{
			InstrumentID: pgUUID(instrumentID), Source: string(source),
			CoveredFrom: pgDate(fetched.From), CoveredThrough: pgDate(fetched.To), UpdatedAt: pgTime(now),
		}))
	})
}

// checkBarKey refuses a key the core must never send: a bug in the caller, not bad client input.
func checkBarKey(instrumentID domain.ID, source domain.BarSource) error {
	if instrumentID.IsZero() {
		return fmt.Errorf("%w: price bars need an instrument id", domain.ErrInternal)
	}
	if _, err := domain.ParseBarSource(string(source)); err != nil {
		return fmt.Errorf("%w: price bars need a known source", domain.ErrInternal)
	}
	return nil
}

func priceBarParams(instrumentID domain.ID, source domain.BarSource, b domain.DailyBar, now time.Time) (sqlc.InsertPriceBarParams, error) {
	var out sqlc.InsertPriceBarParams
	var err error
	if out.Open, err = pgNumeric(b.Open.String()); err != nil {
		return out, err
	}
	if out.High, err = pgNumeric(b.High.String()); err != nil {
		return out, err
	}
	if out.Low, err = pgNumeric(b.Low.String()); err != nil {
		return out, err
	}
	if out.Close, err = pgNumeric(b.Close.String()); err != nil {
		return out, err
	}
	if out.Volume, err = pgNumeric(b.Volume.String()); err != nil {
		return out, err
	}
	out.InstrumentID, out.Source, out.BarDate, out.FetchedAt = pgUUID(instrumentID), string(source), pgDate(b.Date), pgTime(now)
	return out, nil
}

func dailyBarFromRow(row sqlc.AppPriceBar) (domain.DailyBar, error) {
	price := func(field string, n pgtype.Numeric) (domain.Price, error) {
		s, err := numericString(field, n)
		if err != nil {
			return domain.Price{}, err
		}
		p, err := domain.ParsePrice(s)
		return p, fromStored(err)
	}
	var bar domain.DailyBar
	var err error
	bar.Date = tradingDateFromPG(row.BarDate)
	if bar.Open, err = price("price_bars.open", row.Open); err != nil {
		return domain.DailyBar{}, err
	}
	if bar.High, err = price("price_bars.high", row.High); err != nil {
		return domain.DailyBar{}, err
	}
	if bar.Low, err = price("price_bars.low", row.Low); err != nil {
		return domain.DailyBar{}, err
	}
	if bar.Close, err = price("price_bars.close", row.Close); err != nil {
		return domain.DailyBar{}, err
	}
	volume, err := numericString("price_bars.volume", row.Volume)
	if err != nil {
		return domain.DailyBar{}, err
	}
	if bar.Volume, err = domain.ParseVolume(volume); err != nil {
		return domain.DailyBar{}, fromStored(err)
	}
	return domain.NewDailyBar(bar)
}

// pgDate maps a trading date to a date column; the zero date becomes NULL.
func pgDate(d domain.TradingDate) pgtype.Date {
	if d.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: d.UTC(), Valid: true}
}

// tradingDateFromPG maps a date column to a trading date; NULL becomes the zero date.
func tradingDateFromPG(d pgtype.Date) domain.TradingDate {
	if !d.Valid {
		return domain.TradingDate{}
	}
	y, m, day := d.Time.Date()
	return domain.NewTradingDate(y, m, day)
}
