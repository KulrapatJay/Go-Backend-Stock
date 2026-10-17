package domain

import "strconv"

const (
	DefaultPageLimit = 20
	MaxPageLimit     = 100
	MaxCursorBytes   = 1024
)

// Page selects one page of a list. The cursor is opaque here: adapters encode and decode it.
type Page struct {
	limit  int
	cursor string
}

// ParsePage reads the limit and cursor query values. An empty limit means DefaultPageLimit.
func ParsePage(rawLimit, cursor string) (Page, error) {
	limit := DefaultPageLimit
	if rawLimit != "" {
		if len(rawLimit) > 3 || !isDigits(rawLimit) {
			return Page{}, invalidInput("limit", "must be an integer from 1 to 100")
		}
		n, err := strconv.Atoi(rawLimit)
		if err != nil || n < 1 || n > MaxPageLimit {
			return Page{}, invalidInput("limit", "must be an integer from 1 to 100")
		}
		limit = n
	}
	if len(cursor) > MaxCursorBytes {
		return Page{}, invalidInput("cursor", "must be at most 1024 bytes")
	}
	return Page{limit: limit, cursor: cursor}, nil
}

// Limit returns the page size; 0 only for the zero value.
func (p Page) Limit() int { return p.limit }

// Cursor returns the opaque cursor, or "" for the first page.
func (p Page) Cursor() string { return p.cursor }

// IsZero reports whether p is the zero value, which has no limit and must not be used.
func (p Page) IsZero() bool { return p.limit == 0 }

// isDigits reports whether s is non-empty and only ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
