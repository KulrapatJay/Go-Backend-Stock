package domain

import (
	"strings"
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
