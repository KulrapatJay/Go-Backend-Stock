package domain

import (
	"crypto/sha256"
	"strings"
	"time"
)

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

// TransactionInput is the part of a transaction a client supplies, already checked.
type TransactionInput struct {
	Symbol     Symbol
	Side       Side
	Quantity   Quantity
	Price      Price
	Fee        Fee
	ExecutedAt time.Time
}

// ParseTransactionInput parses the six fields of a create or update request and returns the
// first error in field order: symbol, side, quantity, price, fee, executed_at. now is the latest
// allowed executed_at.
func ParseTransactionInput(symbol, side, quantity, price, fee, executedAt string, now time.Time) (TransactionInput, error) {
	sym, err := ParseSymbol(symbol)
	if err != nil {
		return TransactionInput{}, err
	}
	sd, err := ParseSide(side)
	if err != nil {
		return TransactionInput{}, err
	}
	q, err := ParseQuantity(quantity)
	if err != nil {
		return TransactionInput{}, err
	}
	p, err := ParsePrice(price)
	if err != nil {
		return TransactionInput{}, err
	}
	f, err := ParseFee(fee)
	if err != nil {
		return TransactionInput{}, err
	}
	at, err := ParseExecutedAt(executedAt, now)
	if err != nil {
		return TransactionInput{}, err
	}
	return TransactionInput{Symbol: sym, Side: sd, Quantity: q, Price: p, Fee: f, ExecutedAt: at}, nil
}

// RequestHash fingerprints the request for Idempotency-Key: SHA-256 of
// symbol\x00side\x00quantity\x00price\x00fee\x00executed_at. Decimals use their canonical String()
// and the time is UTC RFC 3339 with nanoseconds, so formatting differences ("0.50" and "0.5", or
// another UTC offset) give the same hash while any change of value gives a different one.
func (in TransactionInput) RequestHash() []byte {
	canonical := strings.Join([]string{
		in.Symbol.String(), string(in.Side), in.Quantity.String(), in.Price.String(), in.Fee.String(),
		in.ExecutedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return sum[:]
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

// NewTransactionFilter checks the filter a client asked for: From must not be after To. An absent
// filter is nil; a pointer to the zero Symbol or to a side other than buy/sell is a caller bug.
func NewTransactionFilter(page Page, symbol *Symbol, side *Side, from, to *time.Time) (TransactionFilter, error) {
	if page.IsZero() {
		return TransactionFilter{}, invariant("filter.page", "is required")
	}
	if symbol != nil && symbol.IsZero() {
		return TransactionFilter{}, invariant("filter.symbol", "must be nil or a parsed symbol")
	}
	if side != nil && !isOneOf(*side, SideBuy, SideSell) {
		return TransactionFilter{}, invariant("filter.side", "must be nil, buy or sell")
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
