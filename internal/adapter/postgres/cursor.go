package postgres

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"go-backend-stock/internal/core/domain"
)

// Keyset cursors (milestone 3 spec section 6.1). A cursor is the base64url JSON of the last row's
// sort key plus the list kind and a fingerprint of what the list is scoped to (the owner for
// portfolios, the portfolio and filters for transactions), so a cursor only continues the list it
// came from. Cursors come from clients: anything that does not decode exactly is invalid_input.

const (
	cursorKindPortfolios   = "portfolios"
	cursorKindTransactions = "transactions"
)

var errInvalidCursor = domain.NewError(domain.CodeInvalidInput, "cursor is invalid or does not match the current filters")

type cursorPosition struct {
	Kind     string    `json:"k"`
	Filter   string    `json:"f"`
	At       time.Time `json:"t"`
	ID       string    `json:"i,omitempty"` // portfolios: the last id
	Sequence int64     `json:"s,omitempty"` // transactions: the last sequence
}

func encodeCursor(p cursorPosition) string {
	b, err := json.Marshal(p)
	if err != nil {
		panic("encode cursor: " + err.Error()) // only strings, a time and an int: cannot fail
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor returns the position in raw, or errInvalidCursor when raw is malformed or belongs
// to another list kind or filter.
func decodeCursor(raw, kind, filter string) (cursorPosition, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursorPosition{}, errInvalidCursor
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var p cursorPosition
	if err := dec.Decode(&p); err != nil || dec.More() {
		return cursorPosition{}, errInvalidCursor
	}
	if p.Kind != kind || p.Filter != filter || p.At.IsZero() {
		return cursorPosition{}, errInvalidCursor
	}
	return p, nil
}

// fingerprint identifies what a list is scoped to: the first 8 bytes of the SHA-256 of the parts.
func fingerprint(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}
