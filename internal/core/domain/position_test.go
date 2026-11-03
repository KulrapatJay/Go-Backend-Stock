package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// Every expected value below is worked out by hand from the AGENTS.md rules, not taken from the
// implementation. Position's fields are decimals, so they are compared with Equal (value) and the
// canonical String() is checked where the exact digits matter.

func tradeOf(t *testing.T, side Side, qty, price, fee string) TradeEvent {
	t.Helper()
	q, err := ParseQuantity(qty)
	if err != nil {
		t.Fatalf("ParseQuantity(%q) error = %v", qty, err)
	}
	p, err := ParsePrice(price)
	if err != nil {
		t.Fatalf("ParsePrice(%q) error = %v", price, err)
	}
	f, err := ParseFee(fee)
	if err != nil {
		t.Fatalf("ParseFee(%q) error = %v", fee, err)
	}
	return TradeEvent{Side: side, Quantity: q, Price: p, Fee: f}
}

func txOf(t *testing.T, instrument ID, side Side, qty, price, fee string) Transaction {
	t.Helper()
	e := tradeOf(t, side, qty, price, fee)
	return Transaction{InstrumentID: instrument, Side: e.Side, Quantity: e.Quantity, Price: e.Price, Fee: e.Fee}
}

func mustApply(t *testing.T, p Position, e PositionEvent) Position {
	t.Helper()
	next, err := p.Apply(e)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	return next
}

func requirePosition(t *testing.T, label string, got Position, quantity, cost, average, realized string) {
	t.Helper()
	if got.Quantity.String() != quantity || got.CostBasis.String() != cost ||
		got.AverageCost.String() != average || got.RealizedPnL.String() != realized {
		t.Errorf("%s = {qty %s, cost %s, avg %s, realized %s}, want {qty %s, cost %s, avg %s, realized %s}", label,
			got.Quantity, got.CostBasis, got.AverageCost, got.RealizedPnL, quantity, cost, average, realized)
	}
}

func requireInsufficient(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInsufficientShares) {
		t.Fatalf("error = %v, want ErrInsufficientShares", err)
	}
}

func TestPositionGoldenExample(t *testing.T) {
	// Spec section 12: buy 2 x 100 with fee 2 costs 202; selling 0.5 x 120 with fee 1 allocates
	// 202 x 0.5 / 2 = 50.5 and realizes 60 - 1 - 50.5 = 8.5.
	p := mustApply(t, Position{}, tradeOf(t, SideBuy, "2", "100", "2"))
	requirePosition(t, "after buy", p, "2", "202", "101", "0")

	p = mustApply(t, p, tradeOf(t, SideSell, "0.5", "120", "1"))
	requirePosition(t, "after sell", p, "1.5", "151.5", "101", "8.5")
}

func TestApplyReturnsEachStep(t *testing.T) {
	events := []PositionEvent{
		tradeOf(t, SideBuy, "10", "5", "0"),    // qty 10, cost 50, avg 5
		tradeOf(t, SideBuy, "10", "7", "1"),    // cost 50 + 70 + 1 = 121, qty 20, avg 6.05
		tradeOf(t, SideSell, "5", "8", "0.5"),  // allocated 121 x 5 / 20 = 30.25; realized 40 - 0.5 - 30.25 = 9.25
		tradeOf(t, SideSell, "15", "6", "0.1"), // sells the rest: allocated 90.75; realized 90 - 0.1 - 90.75 = -0.85
	}
	want := []struct{ qty, cost, avg, realized string }{
		{"10", "50", "5", "0"},
		{"20", "121", "6.05", "0"},
		{"15", "90.75", "6.05", "9.25"},
		{"0", "0", "0", "8.4"}, // 9.25 + (-0.85)
	}
	p := Position{}
	for i, e := range events {
		before := p
		next, err := p.Apply(e)
		if err != nil {
			t.Fatalf("event %d: Apply() error = %v", i, err)
		}
		requirePosition(t, "step", next, want[i].qty, want[i].cost, want[i].avg, want[i].realized)
		// Apply must not change the receiver.
		if !p.Quantity.Equal(before.Quantity) || !p.CostBasis.Equal(before.CostBasis) ||
			!p.AverageCost.Equal(before.AverageCost) || !p.RealizedPnL.Equal(before.RealizedPnL) {
			t.Errorf("event %d: Apply() changed its receiver: %+v -> %+v", i, before, p)
		}
		p = next
	}

	replayed, err := ReplayPosition(events)
	if err != nil {
		t.Fatalf("ReplayPosition() error = %v", err)
	}
	requirePosition(t, "ReplayPosition", replayed, "0", "0", "0", "8.4")

	empty, err := ReplayPosition(nil)
	if err != nil {
		t.Fatalf("ReplayPosition(nil) error = %v", err)
	}
	requirePosition(t, "ReplayPosition(nil)", empty, "0", "0", "0", "0")
}

func TestSellMoreThanHeldIsRefused(t *testing.T) {
	held := mustApply(t, Position{}, tradeOf(t, SideBuy, "1", "10", "0"))
	got, err := held.Apply(tradeOf(t, SideSell, "1.0000000001", "10", "0"))
	requireInsufficient(t, err)
	requirePosition(t, "position after refused sale", got, "1", "10", "10", "0")

	// Selling exactly what is held is allowed.
	if _, err := held.Apply(tradeOf(t, SideSell, "1", "10", "0")); err != nil {
		t.Errorf("selling exactly the held quantity error = %v", err)
	}

	// Nothing held at all, including after a position was closed.
	_, err = Position{}.Apply(tradeOf(t, SideSell, "1", "10", "0"))
	requireInsufficient(t, err)

	closed := mustApply(t, held, tradeOf(t, SideSell, "1", "12", "0"))
	_, err = closed.Apply(tradeOf(t, SideSell, "0.0000000001", "12", "0"))
	requireInsufficient(t, err)

	_, err = ReplayPosition([]PositionEvent{
		tradeOf(t, SideBuy, "1", "10", "0"),
		tradeOf(t, SideSell, "2", "10", "0"),
	})
	requireInsufficient(t, err)
}

func TestFullSaleLeavesNoResidualAfterThirds(t *testing.T) {
	// Buy 3 x 10 with fee 0.01: cost 30.01. Selling a third at a time cannot divide evenly.
	p := mustApply(t, Position{}, tradeOf(t, SideBuy, "3", "10", "0.01"))
	requirePosition(t, "after buy", p, "3", "30.01", "10.0033333333333333333333333333", "0")

	// Sale 1: allocated 30.01 / 3 = 10.00333... -> 10.0033333333333333333333333333 (28 places);
	// realized 12 - allocated = 1.9966666666666666666666666667; cost 20.0066666666666666666666666667.
	p = mustApply(t, p, tradeOf(t, SideSell, "1", "12", "0"))
	requirePosition(t, "after sale 1", p, "2", "20.0066666666666666666666666667",
		"10.0033333333333333333333333334", "1.9966666666666666666666666667")

	// Sale 2: 20.0066666666666666666666666667 / 2 is exactly ...33335, a tie, and 3 is odd, so the
	// allocation rounds up to 10.0033333333333333333333333334. Cost left: ...3333 (28 places).
	p = mustApply(t, p, tradeOf(t, SideSell, "1", "12", "0"))
	requirePosition(t, "after sale 2", p, "1", "10.0033333333333333333333333333",
		"10.0033333333333333333333333333", "3.9933333333333333333333333333")

	// Sale 3 sells everything: allocated is the whole remaining cost, so nothing is left over and
	// the three allocations add up to exactly 30.01. Realized = 3 x 12 - 30.01 = 5.99.
	p = mustApply(t, p, tradeOf(t, SideSell, "1", "12", "0"))
	if !p.Quantity.IsZero() || !p.CostBasis.IsZero() || !p.AverageCost.IsZero() {
		t.Errorf("after full sale = {qty %s, cost %s, avg %s}, want all zero", p.Quantity, p.CostBasis, p.AverageCost)
	}
	if p.RealizedPnL.String() != "5.99" {
		t.Errorf("realized = %s, want 5.99", p.RealizedPnL)
	}

	// A new buy starts from zero cost and realized P&L stays.
	p = mustApply(t, p, tradeOf(t, SideBuy, "2", "50", "0"))
	requirePosition(t, "after rebuy", p, "2", "100", "50", "5.99")
}

func TestRealizedLossIsNegative(t *testing.T) {
	// Buy 10 x 100 (cost 1000); sell 4 x 90 with fee 2: allocated 400, realized 360 - 2 - 400 = -42.
	p := mustApply(t, Position{}, tradeOf(t, SideBuy, "10", "100", "0"))
	p = mustApply(t, p, tradeOf(t, SideSell, "4", "90", "2"))
	requirePosition(t, "after loss", p, "6", "600", "100", "-42")
	if !p.RealizedPnL.IsNegative() {
		t.Errorf("RealizedPnL = %s, want negative", p.RealizedPnL)
	}
}

func TestDivideHalfEven(t *testing.T) {
	oneUnit := decimal.New(1, -28)
	third := strings.Repeat("3", 28)
	twoThirds := strings.Repeat("6", 27) + "7"

	for name, tc := range map[string]struct {
		a, b decimal.Decimal
		want string
	}{
		"one third":            {decimal.NewFromInt(1), decimal.NewFromInt(3), "0." + third},
		"two thirds":           {decimal.NewFromInt(2), decimal.NewFromInt(3), "0." + twoThirds},
		"below half unit":      {decimal.New(4, -29), decimal.NewFromInt(1), "0"},
		"tie to even zero":     {decimal.New(5, -29), decimal.NewFromInt(1), "0"},
		"tie rounds up to 2":   {decimal.New(15, -29), decimal.NewFromInt(1), oneUnit.Mul(decimal.NewFromInt(2)).String()},
		"tie stays at 2":       {decimal.New(25, -29), decimal.NewFromInt(1), oneUnit.Mul(decimal.NewFromInt(2)).String()},
		"tie rounds up to 4":   {decimal.New(35, -29), decimal.NewFromInt(1), oneUnit.Mul(decimal.NewFromInt(4)).String()},
		"above half goes up":   {decimal.New(26, -29), decimal.NewFromInt(1), oneUnit.Mul(decimal.NewFromInt(3)).String()},
		"below half goes down": {decimal.New(24, -29), decimal.NewFromInt(1), oneUnit.Mul(decimal.NewFromInt(2)).String()},
		"exact division":       {decimal.NewFromInt(10), decimal.NewFromInt(4), "2.5"},
		"zero numerator":       {decimal.Zero, decimal.NewFromInt(7), "0"},
		"exact eighth":         {decimal.NewFromInt(1), decimal.NewFromInt(8), "0.125"},
	} {
		got := divideHalfEven(tc.a, tc.b)
		want, err := decimal.NewFromString(tc.want)
		if err != nil {
			t.Fatalf("%s: bad want %q: %v", name, tc.want, err)
		}
		if !got.Equal(want) {
			t.Errorf("%s: divideHalfEven(%s, %s) = %s, want %s", name, tc.a, tc.b, got, want)
		}
	}

	// It must not depend on or change the library's global division precision.
	before := decimal.DivisionPrecision
	_ = divideHalfEven(decimal.NewFromInt(1), decimal.NewFromInt(3))
	if decimal.DivisionPrecision != before {
		t.Errorf("DivisionPrecision changed from %d to %d", before, decimal.DivisionPrecision)
	}
}

func TestReplayHistoryKeepsInstrumentsApart(t *testing.T) {
	a, b := mustNewID(t), mustNewID(t)
	history := []Transaction{
		txOf(t, a, SideBuy, "2", "10", "0"),  // A: qty 2, cost 20
		txOf(t, b, SideBuy, "1", "20", "0"),  // B: qty 1, cost 20
		txOf(t, a, SideSell, "1", "15", "0"), // A: allocated 10, realized 5, qty 1, cost 10
		txOf(t, b, SideBuy, "1", "30", "1"),  // B: cost 20 + 31 = 51, qty 2, avg 25.5
	}
	got, err := ReplayHistory(history)
	if err != nil {
		t.Fatalf("ReplayHistory() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ReplayHistory() has %d instruments, want 2", len(got))
	}
	requirePosition(t, "instrument A", got[a], "1", "10", "10", "5")
	requirePosition(t, "instrument B", got[b], "2", "51", "25.5", "0")

	// One instrument selling more than it holds fails the whole history, even though the other
	// instrument alone would be fine; B holds 1 here, not the 3 that A's buys might suggest.
	bad := []Transaction{
		txOf(t, a, SideBuy, "5", "10", "0"),
		txOf(t, b, SideBuy, "1", "20", "0"),
		txOf(t, b, SideSell, "3", "20", "0"),
	}
	positions, err := ReplayHistory(bad)
	requireInsufficient(t, err)
	if positions != nil {
		t.Errorf("ReplayHistory() positions = %v on error, want nil", positions)
	}

	none, err := ReplayHistory(nil)
	if err != nil || len(none) != 0 {
		t.Errorf("ReplayHistory(nil) = %v, %v; want an empty map and no error", none, err)
	}
}

func mustNewID(t *testing.T) ID {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	return id
}
