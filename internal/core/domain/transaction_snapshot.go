package domain

import (
	"encoding/json"
	"strconv"
	"time"
)

// transactionSnapshot is the stored JSON form of a Transaction (the idempotency result). Every
// field is required; numbers are strings so no precision is lost, and times are UTC RFC 3339.
// Pointers tell a missing field from an empty one.
type transactionSnapshot struct {
	ID           *string `json:"id"`
	PortfolioID  *string `json:"portfolio_id"`
	InstrumentID *string `json:"instrument_id"`
	Symbol       *string `json:"symbol"`
	Sequence     *string `json:"sequence"`
	Side         *string `json:"side"`
	Quantity     *string `json:"quantity"`
	Price        *string `json:"price"`
	Fee          *string `json:"fee"`
	ExecutedAt   *string `json:"executed_at"`
	Version      *int64  `json:"version"`
	CreatedAt    *string `json:"created_at"`
	UpdatedAt    *string `json:"updated_at"`
}

// MarshalTransactionSnapshot encodes every field of t as JSON for the idempotency record.
func MarshalTransactionSnapshot(t Transaction) (json.RawMessage, error) {
	version := int64(t.Version.Int32())
	s := transactionSnapshot{
		ID: ptr(t.ID.String()), PortfolioID: ptr(t.PortfolioID.String()), InstrumentID: ptr(t.InstrumentID.String()),
		Symbol: ptr(t.Symbol.String()), Sequence: ptr(strconv.FormatInt(t.Sequence, 10)), Side: ptr(string(t.Side)),
		Quantity: ptr(t.Quantity.String()), Price: ptr(t.Price.String()), Fee: ptr(t.Fee.String()),
		ExecutedAt: ptr(formatSnapshotTime(t.ExecutedAt)), Version: &version,
		CreatedAt: ptr(formatSnapshotTime(t.CreatedAt)), UpdatedAt: ptr(formatSnapshotTime(t.UpdatedAt)),
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, invariant("transaction snapshot", "cannot be encoded")
	}
	return raw, nil
}

// ParseTransactionSnapshot decodes a snapshot written by MarshalTransactionSnapshot. The snapshot
// is data the server stored itself, so a missing field or a value that does not parse is a bug or
// corruption: the error is internal and never repeats the stored value.
func ParseTransactionSnapshot(raw json.RawMessage) (Transaction, error) {
	var s transactionSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return Transaction{}, invariant("transaction snapshot", "is not valid JSON of the expected shape")
	}
	for name, missing := range map[string]bool{
		"id": s.ID == nil, "portfolio_id": s.PortfolioID == nil, "instrument_id": s.InstrumentID == nil,
		"symbol": s.Symbol == nil, "sequence": s.Sequence == nil, "side": s.Side == nil,
		"quantity": s.Quantity == nil, "price": s.Price == nil, "fee": s.Fee == nil,
		"executed_at": s.ExecutedAt == nil, "version": s.Version == nil,
		"created_at": s.CreatedAt == nil, "updated_at": s.UpdatedAt == nil,
	} {
		if missing {
			return Transaction{}, invariant("transaction snapshot."+name, "is missing")
		}
	}

	var t Transaction
	var err error
	bad := func(field string) (Transaction, error) {
		return Transaction{}, invariant("transaction snapshot."+field, "is not valid")
	}
	if t.ID, err = ParseID("id", *s.ID); err != nil {
		return bad("id")
	}
	if t.PortfolioID, err = ParseID("portfolio_id", *s.PortfolioID); err != nil {
		return bad("portfolio_id")
	}
	if t.InstrumentID, err = ParseID("instrument_id", *s.InstrumentID); err != nil {
		return bad("instrument_id")
	}
	if t.Symbol, err = ParseSymbol(*s.Symbol); err != nil {
		return bad("symbol")
	}
	if t.Sequence, err = strconv.ParseInt(*s.Sequence, 10, 64); err != nil {
		return bad("sequence")
	}
	if t.Side, err = ParseSide(*s.Side); err != nil {
		return bad("side")
	}
	if t.Quantity, err = ParseQuantity(*s.Quantity); err != nil {
		return bad("quantity")
	}
	if t.Price, err = ParsePrice(*s.Price); err != nil {
		return bad("price")
	}
	if t.Fee, err = ParseFee(*s.Fee); err != nil {
		return bad("fee")
	}
	if t.ExecutedAt, err = parseTimestamp("executed_at", *s.ExecutedAt); err != nil {
		return bad("executed_at")
	}
	if t.Version, err = ParseVersion(*s.Version); err != nil {
		return bad("version")
	}
	if t.CreatedAt, err = parseTimestamp("created_at", *s.CreatedAt); err != nil {
		return bad("created_at")
	}
	if t.UpdatedAt, err = parseTimestamp("updated_at", *s.UpdatedAt); err != nil {
		return bad("updated_at")
	}
	return t, nil
}

func formatSnapshotTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func ptr[T any](v T) *T { return &v }
