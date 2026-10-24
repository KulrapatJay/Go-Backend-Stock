package domain

import (
	"encoding/json"
	"time"
	"unicode/utf8"
)

const (
	maxIdempotencyKeyBytes    = 128
	MaxIdempotencyResultBytes = 16 * 1024
)

// IdempotencyOperation names the operation a key protects (idempotency_keys.operation CHECK).
type IdempotencyOperation string

const IdempotencyOperationCreateTransaction IdempotencyOperation = "transactions.create"

// ParseIdempotencyOperation accepts exactly "transactions.create".
func ParseIdempotencyOperation(s string) (IdempotencyOperation, error) {
	return parseEnum("operation", s, IdempotencyOperationCreateTransaction)
}

// IdempotencyScope binds a key to one user, portfolio and operation.
type IdempotencyScope struct {
	UserID      ID
	PortfolioID ID
	Operation   IdempotencyOperation
}

// ParseIdempotencyKey checks the Idempotency-Key header: 1-128 visible ASCII characters.
// It returns the key unchanged because repositories take it as a string (spec section 8).
func ParseIdempotencyKey(s string) (string, error) {
	if !isVisibleASCII(s, maxIdempotencyKeyBytes) {
		return "", invalidInput("Idempotency-Key", "must be 1-128 visible ASCII characters")
	}
	return s, nil
}

// IdempotencyRecord stores the result of the first request made with a key.
type IdempotencyRecord struct {
	Scope       IdempotencyScope
	Key         string
	RequestHash []byte
	Result      json.RawMessage
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// NewIdempotencyRecord checks a record the service is about to save.
func NewIdempotencyRecord(r IdempotencyRecord) (IdempotencyRecord, error) {
	err := firstError(
		requireID("idempotency.user_id", r.Scope.UserID),
		requireID("idempotency.portfolio_id", r.Scope.PortfolioID),
		requireHash("idempotency.request_hash", r.RequestHash),
		requireExpiry(r.CreatedAt, r.ExpiresAt),
	)
	switch {
	case err != nil:
	case !isOneOf(r.Scope.Operation, IdempotencyOperationCreateTransaction):
		err = invariant("idempotency.operation", "must be transactions.create")
	case !isVisibleASCII(r.Key, maxIdempotencyKeyBytes):
		err = invariant("idempotency.key", "must be 1-128 visible ASCII characters")
	case len(r.Result) > MaxIdempotencyResultBytes || !isStorableJSON(r.Result):
		err = invariant("idempotency.result", "must be JSON of at most 16 KiB that PostgreSQL jsonb can store")
	}
	if err != nil {
		return IdempotencyRecord{}, err
	}
	return r, nil
}

// isVisibleASCII reports whether s is 1..maxBytes characters in '!'..'~'.
func isVisibleASCII(s string, maxBytes int) bool {
	if s == "" || len(s) > maxBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '!' || s[i] > '~' {
			return false
		}
	}
	return utf8.ValidString(s)
}
