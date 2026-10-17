package domain

import (
	"regexp"
	"strings"
)

var symbolPattern = regexp.MustCompile(`^[A-Z][A-Z0-9.-]{0,14}$`)

// Symbol is a US stock ticker in upper case, such as "AAPL" or "BRK.B".
type Symbol struct{ value string }

// ParseSymbol trims and upper-cases s, then requires ^[A-Z][A-Z0-9.-]{0,14}$. Non-ASCII input is
// rejected before upper-casing, because characters such as "ı" upper-case to ASCII letters.
func ParseSymbol(s string) (Symbol, error) {
	v := strings.TrimSpace(s)
	if !isASCII(v) {
		return Symbol{}, invalidInput("symbol", "must contain only ASCII letters, digits, '.' or '-'")
	}
	v = strings.ToUpper(v)
	if !symbolPattern.MatchString(v) {
		return Symbol{}, invalidInput("symbol", "must be 1-15 characters: a letter, then letters, digits, '.' or '-'")
	}
	return Symbol{value: v}, nil
}

func (s Symbol) String() string { return s.value }

// IsZero reports whether s is the zero value (no symbol).
func (s Symbol) IsZero() bool { return s.value == "" }
