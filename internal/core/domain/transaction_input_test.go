package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

var inputNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func TestParseTransactionInput(t *testing.T) {
	in, err := domain.ParseTransactionInput("AAPL", "buy", "0.5", "200", "1", "2026-10-01T14:30:00Z", inputNow)
	if err != nil {
		t.Fatalf("ParseTransactionInput(spec example) error = %v", err)
	}
	if in.Symbol.String() != "AAPL" || in.Side != domain.SideBuy || in.Quantity.String() != "0.5" ||
		in.Price.String() != "200" || in.Fee.String() != "1" ||
		!in.ExecutedAt.Equal(time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC)) {
		t.Errorf("ParseTransactionInput(spec example) = %+v", in)
	}

	in, err = domain.ParseTransactionInput(" aapl ", "sell", "1", "2", "0", "2026-10-01T21:30:00+07:00", inputNow)
	if err != nil || in.Symbol.String() != "AAPL" || in.ExecutedAt.Location() != time.UTC {
		t.Errorf("ParseTransactionInput(' aapl ') = %+v, %v; want symbol AAPL and a UTC time", in, err)
	}

	for name, tc := range map[string]struct {
		args  [6]string
		field string
	}{
		"bad symbol":      {[6]string{"1x", "buy", "1", "2", "0", "2026-10-01T14:30:00Z"}, "symbol"},
		"upper-case side": {[6]string{"AAPL", "SELL", "1", "2", "0", "2026-10-01T14:30:00Z"}, "side"},
		"zero quantity":   {[6]string{"AAPL", "buy", "0", "2", "0", "2026-10-01T14:30:00Z"}, "quantity"},
		"empty price":     {[6]string{"AAPL", "buy", "1", "", "0", "2026-10-01T14:30:00Z"}, "price"},
		"negative fee":    {[6]string{"AAPL", "buy", "1", "2", "-1", "2026-10-01T14:30:00Z"}, "fee"},
		"future":          {[6]string{"AAPL", "buy", "1", "2", "0", "2026-10-16T12:00:01Z"}, "executed_at"},
		"no timezone":     {[6]string{"AAPL", "buy", "1", "2", "0", "2026-10-01T14:30:00"}, "executed_at"},
		"missing time":    {[6]string{"AAPL", "buy", "1", "2", "0", ""}, "executed_at"},
	} {
		t.Run(name, func(t *testing.T) {
			a := tc.args
			_, err := domain.ParseTransactionInput(a[0], a[1], a[2], a[3], a[4], a[5], inputNow)
			requireInvalidInput(t, err, tc.field)
		})
	}

	// The first error in field order wins.
	_, err = domain.ParseTransactionInput("1x", "SELL", "0", "", "-1", "", inputNow)
	requireInvalidInput(t, err, "symbol")
	_, err = domain.ParseTransactionInput("AAPL", "buy", "0", "", "-1", "", inputNow)
	requireInvalidInput(t, err, "quantity")
}

func hashOf(t *testing.T, symbol, side, quantity, price, fee, executedAt string) []byte {
	t.Helper()
	in, err := domain.ParseTransactionInput(symbol, side, quantity, price, fee, executedAt, inputNow)
	if err != nil {
		t.Fatalf("ParseTransactionInput(%q, %q, %q, %q, %q, %q) error = %v", symbol, side, quantity, price, fee, executedAt, err)
	}
	return in.RequestHash()
}

func TestRequestHashIgnoresFormatting(t *testing.T) {
	base := hashOf(t, "AAPL", "buy", "0.5", "200", "1", "2026-10-01T14:30:00Z")
	if len(base) != domain.HashSize {
		t.Fatalf("len(hash) = %d, want %d", len(base), domain.HashSize)
	}
	for name, got := range map[string][]byte{
		"trailing zero":  hashOf(t, "AAPL", "buy", "0.50", "200.00", "1.0", "2026-10-01T14:30:00Z"),
		"other offset":   hashOf(t, "AAPL", "buy", "0.5", "200", "1", "2026-10-01T21:30:00+07:00"),
		"lower symbol":   hashOf(t, " aapl", "buy", "0.5", "200", "1", "2026-10-01T14:30:00Z"),
		"leading zeroes": hashOf(t, "AAPL", "buy", "00.5", "0200", "01", "2026-10-01T14:30:00.000000Z"),
	} {
		if string(got) != string(base) {
			t.Errorf("%s: hash differs from the canonical request", name)
		}
	}

	for name, got := range map[string][]byte{
		"symbol":      hashOf(t, "MSFT", "buy", "0.5", "200", "1", "2026-10-01T14:30:00Z"),
		"side":        hashOf(t, "AAPL", "sell", "0.5", "200", "1", "2026-10-01T14:30:00Z"),
		"quantity":    hashOf(t, "AAPL", "buy", "0.6", "200", "1", "2026-10-01T14:30:00Z"),
		"price":       hashOf(t, "AAPL", "buy", "0.5", "201", "1", "2026-10-01T14:30:00Z"),
		"fee":         hashOf(t, "AAPL", "buy", "0.5", "200", "2", "2026-10-01T14:30:00Z"),
		"executed_at": hashOf(t, "AAPL", "buy", "0.5", "200", "1", "2026-10-01T14:30:00.000001Z"),
	} {
		if string(got) == string(base) {
			t.Errorf("changing %s did not change the hash", name)
		}
	}
}

// TestRequestHashSeparatesFields: without the \x00 separator ("AB","C") and ("A","BC") would hash
// the same bytes. Symbols and side cannot hold such values, so the check uses the digit fields:
// quantity "1" + price "23" against quantity "12" + price "3".
func TestRequestHashSeparatesFields(t *testing.T) {
	a := hashOf(t, "AAPL", "buy", "1", "23", "0", "2026-10-01T14:30:00Z")
	b := hashOf(t, "AAPL", "buy", "12", "3", "0", "2026-10-01T14:30:00Z")
	if string(a) == string(b) {
		t.Error("quantity 1 / price 23 collides with quantity 12 / price 3")
	}
	c := hashOf(t, "AB", "buy", "1", "2", "0", "2026-10-01T14:30:00Z")
	d := hashOf(t, "A", "buy", "1", "2", "0", "2026-10-01T14:30:00Z")
	if string(c) == string(d) {
		t.Error("symbols AB and A collide")
	}
}

func snapshotFixture(t *testing.T) domain.Transaction {
	t.Helper()
	sym, _ := domain.ParseSymbol("BRK.B")
	qty, err := domain.ParseQuantity("0.0000000001")
	if err != nil {
		t.Fatal(err)
	}
	price, _ := domain.ParsePrice("123456789012345678.1234567891")
	fee, _ := domain.ParseFee("0")
	ver, _ := domain.ParseVersion(7)
	return domain.Transaction{
		ID: mustID(t), PortfolioID: mustID(t), InstrumentID: mustID(t), Symbol: sym,
		Sequence: 9007199254740993, // beyond the exact range of a JSON double
		Side:     domain.SideSell, Quantity: qty, Price: price, Fee: fee,
		ExecutedAt: time.Date(2026, 10, 1, 14, 30, 0, 123456000, time.UTC),
		Version:    ver,
		CreatedAt:  time.Date(2026, 10, 2, 1, 2, 3, 456789000, time.UTC),
		UpdatedAt:  time.Date(2026, 10, 3, 4, 5, 6, 7000, time.UTC),
	}
}

func TestTransactionSnapshotRoundTrip(t *testing.T) {
	want := snapshotFixture(t)
	raw, err := domain.MarshalTransactionSnapshot(want)
	if err != nil {
		t.Fatalf("MarshalTransactionSnapshot error = %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("snapshot is not valid JSON: %s", raw)
	}
	got, err := domain.ParseTransactionSnapshot(raw)
	if err != nil {
		t.Fatalf("ParseTransactionSnapshot error = %v", err)
	}
	if got.ID != want.ID || got.PortfolioID != want.PortfolioID || got.InstrumentID != want.InstrumentID ||
		got.Symbol != want.Symbol || got.Sequence != want.Sequence || got.Side != want.Side ||
		got.Quantity.String() != want.Quantity.String() || got.Price.String() != want.Price.String() ||
		got.Fee.String() != want.Fee.String() || got.Version != want.Version {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
	if got.Quantity.String() != "0.0000000001" {
		t.Errorf("quantity = %s, want 0.0000000001", got.Quantity)
	}
	for name, pair := range map[string][2]time.Time{
		"executed_at": {got.ExecutedAt, want.ExecutedAt},
		"created_at":  {got.CreatedAt, want.CreatedAt},
		"updated_at":  {got.UpdatedAt, want.UpdatedAt},
	} {
		if !pair[0].Equal(pair[1]) || pair[0].Location() != time.UTC {
			t.Errorf("%s = %v, want %v in UTC", name, pair[0], pair[1])
		}
	}
}

func TestTransactionSnapshotRejectsDamagedJSON(t *testing.T) {
	raw, err := domain.MarshalTransactionSnapshot(snapshotFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 13 {
		t.Errorf("snapshot has %d fields, want all 13 fields of Transaction", len(fields))
	}

	rebuild := func(mutate func(map[string]any)) json.RawMessage {
		m := map[string]any{}
		for k, v := range fields {
			m[k] = v
		}
		mutate(m)
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	cases := map[string]json.RawMessage{
		"not json":  json.RawMessage(`{"id":`),
		"null":      json.RawMessage(`null`),
		"empty":     json.RawMessage(``),
		"an array":  json.RawMessage(`[]`),
		"empty obj": json.RawMessage(`{}`),
	}
	for key := range fields {
		cases["missing "+key] = rebuild(func(m map[string]any) { delete(m, key) })
	}
	for key, bad := range map[string]any{
		"quantity":     "abc",
		"price":        "-1",
		"fee":          "1e3",
		"sequence":     "x",
		"side":         "hold",
		"symbol":       "1x",
		"id":           "not-a-uuid",
		"version":      0,
		"executed_at":  "2026-10-01 14:30:00",
		"created_at":   "2026-10-01T14:30:00",
		"updated_at":   12,
		"portfolio_id": "",
	} {
		cases["bad "+key] = rebuild(func(m map[string]any) { m[key] = bad })
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := domain.ParseTransactionSnapshot(in)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}

func TestTransactionSnapshotErrorsNeverEchoStoredValues(t *testing.T) {
	raw, err := domain.MarshalTransactionSnapshot(snapshotFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	damaged := strings.Replace(string(raw), `"sell"`, `"`+echoMarker+`"`, 1)
	_, err = domain.ParseTransactionSnapshot(json.RawMessage(damaged))
	if err == nil {
		t.Fatal("ParseTransactionSnapshot accepted a damaged side")
	}
	if strings.Contains(strings.ToUpper(err.Error()), echoMarker) {
		t.Errorf("error %q echoes the stored value", err)
	}
}

func TestParseTimeFilter(t *testing.T) {
	for in, want := range map[string]time.Time{
		"2026-10-01T14:30:00Z":        time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC),
		"2026-10-01T21:30:00+07:00":   time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC),
		"2999-01-01T00:00:00Z":        time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC), // the future is allowed
		"1960-01-01T00:00:00Z":        time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC),
		"2026-10-01T14:30:00.123456Z": time.Date(2026, 10, 1, 14, 30, 0, 123456000, time.UTC),
	} {
		got, err := domain.ParseTimeFilter("from", in)
		if err != nil || !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("ParseTimeFilter(%q) = %v, %v; want %v in UTC", in, got, err, want)
		}
	}
	for _, in := range []string{"", "2026-10-01", "2026-10-01T14:30:00", "2026-10-01T14:30:00.1234567Z", "1696170600"} {
		_, err := domain.ParseTimeFilter("to", in)
		requireInvalidInput(t, err, "to")
	}
}
