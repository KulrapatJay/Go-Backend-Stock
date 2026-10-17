package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxPortfolioNameRunes        = 100
	maxPortfolioDescriptionRunes = 1000
)

// PortfolioName is a trimmed, non-empty name of at most 100 characters.
type PortfolioName struct{ value string }

// ParsePortfolioName trims s and counts characters (runes), not bytes, to match varchar(100).
func ParsePortfolioName(s string) (PortfolioName, error) {
	v := strings.TrimSpace(s)
	if !utf8.ValidString(v) {
		return PortfolioName{}, invalidInput("name", "must be valid UTF-8")
	}
	if v == "" {
		return PortfolioName{}, invalidInput("name", "is required")
	}
	if utf8.RuneCountInString(v) > maxPortfolioNameRunes {
		return PortfolioName{}, invalidInput("name", "must be at most 100 characters")
	}
	if hasControl(v) {
		return PortfolioName{}, invalidInput("name", "must not contain control characters")
	}
	return PortfolioName{value: v}, nil
}

func (n PortfolioName) String() string { return n.value }

// IsZero reports whether n is the zero value, which is never a valid name.
func (n PortfolioName) IsZero() bool { return n.value == "" }

// PortfolioDescription is optional text of at most 1000 characters; the zero value is "".
type PortfolioDescription struct{ value string }

// ParsePortfolioDescription keeps s as written (no trimming) and rejects control characters,
// including line breaks.
func ParsePortfolioDescription(s string) (PortfolioDescription, error) {
	if !utf8.ValidString(s) {
		return PortfolioDescription{}, invalidInput("description", "must be valid UTF-8")
	}
	if utf8.RuneCountInString(s) > maxPortfolioDescriptionRunes {
		return PortfolioDescription{}, invalidInput("description", "must be at most 1000 characters")
	}
	if hasControl(s) {
		return PortfolioDescription{}, invalidInput("description", "must not contain control characters")
	}
	return PortfolioDescription{value: s}, nil
}

func (d PortfolioDescription) String() string { return d.value }

// Currency is the portfolio currency. Phase 1 supports only USD (portfolios.currency CHECK).
type Currency string

const CurrencyUSD Currency = "USD"

// ParseCurrency accepts exactly "USD".
func ParseCurrency(s string) (Currency, error) {
	return parseEnum("currency", s, CurrencyUSD)
}

// Portfolio belongs to one user. Its Version covers name and description only.
type Portfolio struct {
	ID          ID
	UserID      ID
	Name        PortfolioName
	Description PortfolioDescription
	Currency    Currency
	Version     Version
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PortfolioPage is one page of a user's portfolios, newest first. NextCursor is "" on the last page.
type PortfolioPage struct {
	Items      []Portfolio
	NextCursor string
}
