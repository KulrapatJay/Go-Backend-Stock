package postgres

import (
	"errors"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func barDate(t *testing.T, s string) domain.TradingDate {
	t.Helper()
	d, err := domain.ParseTradingDate("date", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func newBar(t *testing.T, date, open, high, low, closePrice, volume string) domain.DailyBar {
	t.Helper()
	price := func(s string) domain.Price {
		p, err := domain.ParsePrice(s)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	v, err := domain.ParseVolume(volume)
	if err != nil {
		t.Fatal(err)
	}
	b, err := domain.NewDailyBar(domain.DailyBar{
		Date: barDate(t, date), Open: price(open), High: price(high), Low: price(low), Close: price(closePrice), Volume: v,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rangeOf(t *testing.T, from, to string) domain.DateRange {
	t.Helper()
	return domain.DateRange{From: barDate(t, from), To: barDate(t, to)}
}

func TestPriceBarsSaveThenReadBackExactlyAndOldestFirst(t *testing.T) {
	store := newRuntimeStore(t)
	instrument := insertInstrument(t, store)
	bars := []domain.DailyBar{
		newBar(t, "2026-11-10", "351.35", "353.87", "346.4605", "348.51", "29910098"),
		newBar(t, "2026-11-06", "338.73", "345.79", "338.3", "342.37", "44861923"),
		newBar(t, "2026-11-09", "344.995", "352.33", "344.78", "350.69", "48935297"),
	}

	if err := store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSourceAlpaca, bars, rangeOf(t, "2026-11-06", "2026-11-10"), now()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := store.PriceBars().Range(t.Context(), instrument.ID, domain.BarSourceAlpaca, barDate(t, "2026-11-06"), barDate(t, "2026-11-10"))
	if err != nil || len(got) != 3 {
		t.Fatalf("Range() = %d bars, %v; want 3", len(got), err)
	}
	for i, want := range []string{"2026-11-06", "2026-11-09", "2026-11-10"} {
		if got[i].Date.String() != want {
			t.Errorf("bar %d date = %s, want %s (oldest first)", i, got[i].Date, want)
		}
	}
	last := got[2]
	if last.Open.String() != "351.35" || last.High.String() != "353.87" || last.Low.String() != "346.4605" ||
		last.Close.String() != "348.51" || last.Volume.String() != "29910098" {
		t.Errorf("last bar = %s %s %s %s %s; numbers must come back exactly", last.Open, last.High, last.Low, last.Close, last.Volume)
	}

	part, err := store.PriceBars().Range(t.Context(), instrument.ID, domain.BarSourceAlpaca, barDate(t, "2026-11-09"), barDate(t, "2026-11-09"))
	if err != nil || len(part) != 1 || part[0].Date.String() != "2026-11-09" {
		t.Errorf("Range(one day) = %v, %v", part, err)
	}
	none, err := store.PriceBars().Range(t.Context(), instrument.ID, domain.BarSourceAlpaca, barDate(t, "2026-01-01"), barDate(t, "2026-01-31"))
	if err != nil || len(none) != 0 {
		t.Errorf("Range(before the data) = %v, %v; want none", none, err)
	}
}

func TestPriceBarsCoverageIsAbsentUntilSavedThenFollowsTheFetchedRange(t *testing.T) {
	store := newRuntimeStore(t)
	instrument := insertInstrument(t, store)
	if _, ok, err := store.PriceBars().Coverage(t.Context(), instrument.ID, domain.BarSourceAlpaca); ok || err != nil {
		t.Fatalf("Coverage() before any Save = ok %v, %v; want absent and no error", ok, err)
	}

	// A range of holidays only: no bar, but the range is now known to hold none.
	if err := store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSourceAlpaca, nil, rangeOf(t, "2026-12-24", "2026-12-26"), now()); err != nil {
		t.Fatalf("Save(no bars) error = %v", err)
	}
	c, ok, err := store.PriceBars().Coverage(t.Context(), instrument.ID, domain.BarSourceAlpaca)
	if !ok || err != nil || c.From.String() != "2026-12-24" || c.Through.String() != "2026-12-26" {
		t.Fatalf("Coverage() = %+v, ok %v, %v; want 2026-12-24..2026-12-26", c, ok, err)
	}
}

func TestPriceBarsCoverageGrowsOnlyWhenTheNewRangeTouchesIt(t *testing.T) {
	store := newRuntimeStore(t)
	instrument := insertInstrument(t, store)
	save := func(from, to string, bars ...domain.DailyBar) {
		t.Helper()
		if err := store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSourceAlpaca, bars, rangeOf(t, from, to), now()); err != nil {
			t.Fatalf("Save(%s..%s) error = %v", from, to, err)
		}
	}
	coverage := func() string {
		t.Helper()
		c, ok, err := store.PriceBars().Coverage(t.Context(), instrument.ID, domain.BarSourceAlpaca)
		if !ok || err != nil {
			t.Fatalf("Coverage() = ok %v, %v", ok, err)
		}
		return c.From.String() + ".." + c.Through.String()
	}

	save("2026-03-01", "2026-06-30")
	if got := coverage(); got != "2026-03-01..2026-06-30" {
		t.Fatalf("after the first save coverage = %s", got)
	}
	save("2026-01-01", "2026-02-28") // ends the day before the covered range: touches
	if got := coverage(); got != "2026-01-01..2026-06-30" {
		t.Errorf("a range touching the front: coverage = %s, want 2026-01-01..2026-06-30", got)
	}
	save("2026-07-01", "2026-08-31") // starts the day after: touches
	if got := coverage(); got != "2026-01-01..2026-08-31" {
		t.Errorf("a range touching the back: coverage = %s, want 2026-01-01..2026-08-31", got)
	}
	save("2026-02-01", "2026-03-15") // inside: nothing changes
	if got := coverage(); got != "2026-01-01..2026-08-31" {
		t.Errorf("a range inside: coverage = %s, want it unchanged", got)
	}
	// A range that leaves a gap must not turn the gap into "fetched". Its bars are stored all the same.
	gapBar := newBar(t, "2026-11-10", "10", "11", "9", "10.5", "100")
	save("2026-11-09", "2026-11-12", gapBar)
	if got := coverage(); got != "2026-01-01..2026-08-31" {
		t.Errorf("a range with a gap: coverage = %s, want it unchanged", got)
	}
	got, err := store.PriceBars().Range(t.Context(), instrument.ID, domain.BarSourceAlpaca, barDate(t, "2026-11-01"), barDate(t, "2026-11-30"))
	if err != nil || len(got) != 1 {
		t.Errorf("the bar of the gapped range = %v, %v; want it stored", got, err)
	}
}

func TestPriceBarsAStoredBarIsNeverChanged(t *testing.T) {
	store := newRuntimeStore(t)
	instrument := insertInstrument(t, store)
	first := newBar(t, "2026-11-10", "100", "110", "90", "105", "1000")
	again := newBar(t, "2026-11-10", "1", "2", "1", "2", "5")
	fetched := rangeOf(t, "2026-11-10", "2026-11-10")
	if err := store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSourceAlpaca, []domain.DailyBar{first}, fetched, now()); err != nil {
		t.Fatal(err)
	}
	if err := store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSourceAlpaca, []domain.DailyBar{again}, fetched, now()); err != nil {
		t.Fatalf("saving the same date again error = %v, want success", err)
	}
	got, err := store.PriceBars().Range(t.Context(), instrument.ID, domain.BarSourceAlpaca, fetched.From, fetched.To)
	if err != nil || len(got) != 1 || got[0].Close.String() != "105" || got[0].Volume.String() != "1000" {
		t.Fatalf("Range() = %+v, %v; want the first bar kept", got, err)
	}
}

func TestPriceBarsOfOneSourceAreNeverReadAsAnother(t *testing.T) {
	store := newRuntimeStore(t)
	instrument := insertInstrument(t, store)
	fetched := rangeOf(t, "2026-11-10", "2026-11-10")
	demo := newBar(t, "2026-11-10", "1", "2", "1", "1.5", "10")
	if err := store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSourceDemo, []domain.DailyBar{demo}, fetched, now()); err != nil {
		t.Fatal(err)
	}

	got, err := store.PriceBars().Range(t.Context(), instrument.ID, domain.BarSourceAlpaca, fetched.From, fetched.To)
	if err != nil || len(got) != 0 {
		t.Errorf("alpaca Range() = %v, %v; want none after a demo save", got, err)
	}
	if _, ok, err := store.PriceBars().Coverage(t.Context(), instrument.ID, domain.BarSourceAlpaca); ok || err != nil {
		t.Errorf("alpaca Coverage() = ok %v, %v; want absent after a demo save", ok, err)
	}
	if _, ok, err := store.PriceBars().Coverage(t.Context(), instrument.ID, domain.BarSourceDemo); !ok || err != nil {
		t.Errorf("demo Coverage() = ok %v, %v; want present", ok, err)
	}
}

func TestPriceBarsOfAnotherInstrumentAreNotMixedIn(t *testing.T) {
	store := newRuntimeStore(t)
	a, b := insertInstrument(t, store), insertInstrument(t, store)
	fetched := rangeOf(t, "2026-11-10", "2026-11-10")
	if err := store.PriceBars().Save(t.Context(), a.ID, domain.BarSourceAlpaca, []domain.DailyBar{newBar(t, "2026-11-10", "1", "2", "1", "1.5", "10")}, fetched, now()); err != nil {
		t.Fatal(err)
	}
	got, err := store.PriceBars().Range(t.Context(), b.ID, domain.BarSourceAlpaca, fetched.From, fetched.To)
	if err != nil || len(got) != 0 {
		t.Errorf("Range(other instrument) = %v, %v; want none", got, err)
	}
}

func TestPriceBarsSaveRefusesWhatItCannotStoreWithoutWritingAnything(t *testing.T) {
	store := newRuntimeStore(t)
	instrument := insertInstrument(t, store)
	inside := newBar(t, "2026-11-10", "1", "2", "1", "1.5", "10")
	outside := newBar(t, "2026-11-12", "1", "2", "1", "1.5", "10")
	fetched := rangeOf(t, "2026-11-09", "2026-11-10")

	cases := map[string]func() error{
		"a bar dated outside the fetched range": func() error {
			return store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSourceAlpaca, []domain.DailyBar{inside, outside}, fetched, now())
		},
		"a fetched range that ends before it starts": func() error {
			return store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSourceAlpaca, nil,
				domain.DateRange{From: barDate(t, "2026-11-10"), To: barDate(t, "2026-11-09")}, now())
		},
		"an instrument that does not exist": func() error {
			return store.PriceBars().Save(t.Context(), newID(t), domain.BarSourceAlpaca, []domain.DailyBar{inside}, fetched, now())
		},
		"no instrument id": func() error {
			return store.PriceBars().Save(t.Context(), domain.ID{}, domain.BarSourceAlpaca, []domain.DailyBar{inside}, fetched, now())
		},
		"an unknown source": func() error {
			return store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSource("iex"), []domain.DailyBar{inside}, fetched, now())
		},
	}
	for name, save := range cases {
		t.Run(name, func(t *testing.T) {
			if err := save(); err == nil {
				t.Fatal("Save() succeeded, want an error")
			} else if !errors.Is(err, domain.ErrInternal) {
				t.Errorf("Save() error = %v, want an internal error", err)
			}
		})
	}
	got, err := store.PriceBars().Range(t.Context(), instrument.ID, domain.BarSourceAlpaca, barDate(t, "2026-11-01"), barDate(t, "2026-11-30"))
	if err != nil || len(got) != 0 {
		t.Errorf("after the refused saves Range() = %v, %v; want nothing written", got, err)
	}
	if _, ok, _ := store.PriceBars().Coverage(t.Context(), instrument.ID, domain.BarSourceAlpaca); ok {
		t.Error("a refused save left a coverage row")
	}
}

// TestPriceBarsConcurrentSavesOfTheSameRangeLeaveOneRowPerDay: two requests that both found the same
// days missing both fetch them and both save; the result is the same as one save.
func TestPriceBarsConcurrentSavesOfTheSameRangeLeaveOneRowPerDay(t *testing.T) {
	store := newRuntimeStore(t)
	instrument := insertInstrument(t, store)
	fetched := rangeOf(t, "2026-11-09", "2026-11-10")
	bars := []domain.DailyBar{
		newBar(t, "2026-11-09", "1", "2", "1", "1.5", "10"),
		newBar(t, "2026-11-10", "1", "2", "1", "1.6", "11"),
	}

	succeeded, errs := runConcurrently(8, func() error {
		return store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSourceAlpaca, bars, fetched, now())
	})
	if succeeded != 8 {
		t.Fatalf("%d of 8 concurrent saves succeeded, errors: %v", succeeded, errs)
	}
	got, err := store.PriceBars().Range(t.Context(), instrument.ID, domain.BarSourceAlpaca, fetched.From, fetched.To)
	if err != nil || len(got) != 2 {
		t.Fatalf("Range() = %d bars, %v; want 2", len(got), err)
	}
	c, ok, err := store.PriceBars().Coverage(t.Context(), instrument.ID, domain.BarSourceAlpaca)
	if !ok || err != nil || c.From.String() != "2026-11-09" || c.Through.String() != "2026-11-10" {
		t.Errorf("Coverage() = %+v, ok %v, %v", c, ok, err)
	}
}

// TestPriceBarsConcurrentSavesNeverRecordAGap: two first saves that do not touch each other must not
// leave a coverage that spans the hole between them.
func TestPriceBarsConcurrentSavesNeverRecordAGap(t *testing.T) {
	store := newRuntimeStore(t)
	instrument := insertInstrument(t, store)
	ranges := []domain.DateRange{rangeOf(t, "2020-01-01", "2020-12-31"), rangeOf(t, "2026-01-01", "2026-12-31")}
	next := prepared(ranges)

	succeeded, errs := runConcurrently(len(ranges), func() error {
		return store.PriceBars().Save(t.Context(), instrument.ID, domain.BarSourceAlpaca, nil, next(), now())
	})
	if succeeded != len(ranges) {
		t.Fatalf("%d saves succeeded, errors: %v", succeeded, errs)
	}
	c, ok, err := store.PriceBars().Coverage(t.Context(), instrument.ID, domain.BarSourceAlpaca)
	if !ok || err != nil {
		t.Fatalf("Coverage() = ok %v, %v", ok, err)
	}
	span := c.From.String() + ".." + c.Through.String()
	if span != "2020-01-01..2020-12-31" && span != "2026-01-01..2026-12-31" {
		t.Errorf("coverage = %s; it must be one of the two ranges, never the hole between them", span)
	}
}

func TestPriceBarsTheDatabaseRefusesBadRows(t *testing.T) {
	pool := openPool(t, "TEST_RUNTIME_DATABASE_URL")
	instrument := insertInstrument(t, newRuntimeStore(t))
	insert := `INSERT INTO app.price_bars (instrument_id, source, bar_date, open, high, low, close, volume, fetched_at)
	           VALUES ($1, $2, $3::date, $4::numeric, $5::numeric, $6::numeric, $7::numeric, $8::numeric, now())`
	cases := []struct {
		name                     string
		source, date             string
		open, high, low, cl, vol string
	}{
		{"a good row", "alpaca", "2026-11-10", "1", "2", "1", "1.5", "10"},
		{"an unknown source", "iex", "2026-11-10", "1", "2", "1", "1.5", "10"},
		{"a zero open", "alpaca", "2026-11-11", "0", "2", "1", "1.5", "10"},
		{"a negative close", "alpaca", "2026-11-11", "1", "2", "1", "-1", "10"},
		{"high below low", "alpaca", "2026-11-11", "1", "1", "2", "1", "10"},
		{"a negative volume", "alpaca", "2026-11-11", "1", "2", "1", "1.5", "-1"},
		{"a NaN price", "alpaca", "2026-11-11", "1", "NaN", "1", "1.5", "10"},
		{"an infinite volume", "alpaca", "2026-11-11", "1", "2", "1", "1.5", "Infinity"},
		{"a date before 1970", "alpaca", "1969-12-31", "1", "2", "1", "1.5", "10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), insert, pgUUID(instrument.ID), c.source, c.date, c.open, c.high, c.low, c.cl, c.vol)
			if c.name == "a good row" {
				if err != nil {
					t.Fatalf("insert error = %v, want success", err)
				}
				return
			}
			if err == nil {
				t.Fatal("insert succeeded, want a constraint violation")
			}
		})
	}
}
