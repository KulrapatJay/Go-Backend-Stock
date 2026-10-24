package domain

import (
	"fmt"

	"github.com/google/uuid"
)

const uuidLength = 36

// ID identifies an entity. It is a UUID in canonical lowercase form; the zero value is "no ID".
type ID struct{ value uuid.UUID }

// NewID returns a new UUIDv7, which sorts by creation time and keeps B-tree inserts appending.
func NewID() (ID, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return ID{}, fmt.Errorf("generate id: %w", err)
	}
	return ID{value: u}, nil
}

// ParseID accepts only the canonical 36-character form (any letter case) and rejects the nil UUID.
// field names the value in error messages, e.g. "portfolio_id".
func ParseID(field, s string) (ID, error) {
	if len(s) != uuidLength {
		return ID{}, invalidInput(field, "must be a UUID")
	}
	u, err := uuid.Parse(s)
	if err != nil || u == uuid.Nil {
		return ID{}, invalidInput(field, "must be a UUID")
	}
	return ID{value: u}, nil
}

// String returns the canonical lowercase form, or "" for the zero ID.
func (id ID) String() string {
	if id.IsZero() {
		return ""
	}
	return id.value.String()
}

// IsZero reports whether id is the zero value (no ID).
func (id ID) IsZero() bool { return id.value == uuid.Nil }
