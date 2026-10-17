package domain

import "time"

// Side is the direction of a trade (transactions.side CHECK).
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// ParseSide accepts exactly "buy" or "sell".
func ParseSide(s string) (Side, error) {
	return parseEnum("side", s, SideBuy, SideSell)
}

// Transaction is one recorded trade. Sequence is assigned by the database, never changes and
// breaks ties when ExecutedAt is equal: replay orders by (ExecutedAt, Sequence).
type Transaction struct {
	ID           ID
	PortfolioID  ID
	InstrumentID ID
	Symbol       Symbol // the instrument's current symbol, for responses
	Sequence     int64
	Side         Side
	Quantity     Quantity
	Price        Price
	Fee          Fee
	ExecutedAt   time.Time
	Version      Version
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TransactionFilter narrows a transaction list. Nil fields do not filter.
type TransactionFilter struct {
	Page   Page
	Symbol *Symbol
	Side   *Side
	From   *time.Time
	To     *time.Time
}

// NewTransactionFilter checks the filter a client asked for: From must not be after To.
func NewTransactionFilter(page Page, symbol *Symbol, side *Side, from, to *time.Time) (TransactionFilter, error) {
	if page.IsZero() {
		return TransactionFilter{}, invariant("filter.page", "is required")
	}
	if from != nil && to != nil && from.After(*to) {
		return TransactionFilter{}, invalidInput("from", "must not be after to")
	}
	return TransactionFilter{Page: page, Symbol: symbol, Side: side, From: from, To: to}, nil
}

// TransactionPage is one page of a portfolio's transactions, newest first.
type TransactionPage struct {
	Items      []Transaction
	NextCursor string
}
