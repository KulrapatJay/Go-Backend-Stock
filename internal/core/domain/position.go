package domain

import "github.com/shopspring/decimal"

// divisionPlaces is the number of decimal places kept when dividing costs (AGENTS.md: 28).
const divisionPlaces = 28

// Position is the state of one instrument in one portfolio after replaying its history. The zero
// value means no shares held and nothing realized. It is a calculation result, never stored.
type Position struct {
	Quantity    decimal.Decimal // shares held
	CostBasis   decimal.Decimal // remaining cost of those shares, fees on buys included
	AverageCost decimal.Decimal // CostBasis / Quantity, or zero when nothing is held
	RealizedPnL decimal.Decimal // sum of realized profit and loss over all sales so far
}

// PositionEvent is something that changes a Position. The interface is closed to this package so
// corporate actions can be added later as new event types without touching transactions.
type PositionEvent interface {
	applyTo(Position) (Position, error)
}

// TradeEvent is a buy or a sell of Quantity shares at Price with Fee.
type TradeEvent struct {
	Side     Side
	Quantity Quantity
	Price    Price
	Fee      Fee
}

// NewTradeEvent turns a recorded transaction into the event that replays it.
func NewTradeEvent(t Transaction) TradeEvent {
	return TradeEvent{Side: t.Side, Quantity: t.Quantity, Price: t.Price, Fee: t.Fee}
}

// Apply returns the position after e. It never changes p. A sale of more shares than p holds
// returns ErrInsufficientShares and p unchanged.
func (p Position) Apply(e PositionEvent) (Position, error) {
	if e == nil {
		return p, invariant("event", "is required")
	}
	return e.applyTo(p)
}

// ReplayPosition applies events in the order given, which the caller has already sorted.
func ReplayPosition(events []PositionEvent) (Position, error) {
	p := Position{}
	for _, e := range events {
		next, err := p.Apply(e)
		if err != nil {
			return Position{}, err
		}
		p = next
	}
	return p, nil
}

// ReplayHistory replays a portfolio's transactions, already sorted by (executed_at, sequence), and
// returns one position per instrument. Instruments are calculated apart: a trade in one never
// affects another. It returns ErrInsufficientShares if any instrument would go negative.
func ReplayHistory(history []Transaction) (map[ID]Position, error) {
	positions := make(map[ID]Position)
	for _, t := range history {
		next, err := positions[t.InstrumentID].Apply(NewTradeEvent(t))
		if err != nil {
			return nil, err
		}
		positions[t.InstrumentID] = next
	}
	return positions, nil
}

func (e TradeEvent) applyTo(p Position) (Position, error) {
	switch e.Side {
	case SideBuy:
		return e.applyBuy(p), nil
	case SideSell:
		return e.applySell(p)
	default:
		return p, invariant("side", "must be buy or sell")
	}
}

// applyBuy adds the shares and quantity x price + fee to the remaining cost.
func (e TradeEvent) applyBuy(p Position) Position {
	qty := p.Quantity.Add(e.Quantity.Decimal())
	cost := p.CostBasis.Add(e.Quantity.Decimal().Mul(e.Price.Decimal())).Add(e.Fee.Decimal())
	return Position{Quantity: qty, CostBasis: cost, AverageCost: divideHalfEven(cost, qty), RealizedPnL: p.RealizedPnL}
}

// applySell allocates cost x sold / held to the shares sold. Selling everything allocates all of
// the remaining cost, so no rounding residue is left behind.
func (e TradeEvent) applySell(p Position) (Position, error) {
	sold := e.Quantity.Decimal()
	if !p.Quantity.IsPositive() || sold.GreaterThan(p.Quantity) {
		return p, ErrInsufficientShares
	}

	var allocated decimal.Decimal
	if sold.Equal(p.Quantity) {
		allocated = p.CostBasis
	} else {
		allocated = divideHalfEven(p.CostBasis.Mul(sold), p.Quantity)
	}

	qty := p.Quantity.Sub(sold)
	cost := p.CostBasis.Sub(allocated)
	proceeds := sold.Mul(e.Price.Decimal()).Sub(e.Fee.Decimal())
	next := Position{Quantity: qty, CostBasis: cost, RealizedPnL: p.RealizedPnL.Add(proceeds.Sub(allocated))}
	if qty.IsZero() {
		next.CostBasis = decimal.Zero
		next.AverageCost = decimal.Zero
	} else {
		next.AverageCost = divideHalfEven(cost, qty)
	}
	return next, nil
}

// divideHalfEven returns a / b rounded to 28 decimal places, ties to the even last digit. It needs
// a >= 0 and b > 0. It uses quotient and remainder, so it does not depend on shopspring's global
// DivisionPrecision and does not use DivRound.
func divideHalfEven(a, b decimal.Decimal) decimal.Decimal {
	q, r := a.QuoRem(b, divisionPlaces)
	switch r.Shift(divisionPlaces).Mul(decimal.NewFromInt(2)).Cmp(b) {
	case 1:
		q = q.Add(decimal.New(1, -divisionPlaces))
	case 0:
		if q.Shift(divisionPlaces).Mod(decimal.NewFromInt(2)).IsPositive() { // last digit is odd
			q = q.Add(decimal.New(1, -divisionPlaces))
		}
	}
	return q
}
