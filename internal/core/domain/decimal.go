package domain

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Limits of numeric(28,10): user-entered values are rejected, never rounded, when they exceed them.
const (
	maxIntegerDigits  = 18
	maxFractionDigits = 10
)

// Quantity is a number of shares; fractional shares are allowed. Always greater than zero.
type Quantity struct{ value decimal.Decimal }

// Price is a per-share price in USD. Always greater than zero.
type Price struct{ value decimal.Decimal }

// Fee is a trade fee in USD. Zero or greater; the zero value is a valid fee of 0.
type Fee struct{ value decimal.Decimal }

// ParseQuantity parses a plain decimal string such as "0.5".
func ParseQuantity(s string) (Quantity, error) {
	d, err := parsePositive("quantity", s)
	return Quantity{value: d}, err
}

// ParsePrice parses a plain decimal string such as "200.15".
func ParsePrice(s string) (Price, error) {
	d, err := parsePositive("price", s)
	return Price{value: d}, err
}

// ParseFee parses a plain decimal string such as "1" or "0".
func ParseFee(s string) (Fee, error) {
	d, err := parseDecimal("fee", s)
	if err != nil {
		return Fee{}, err
	}
	return Fee{value: d}, nil
}

// Decimal returns the value for arithmetic.
func (q Quantity) Decimal() decimal.Decimal { return q.value }

// String returns the canonical form: no exponent and no trailing zeros ("0.50" -> "0.5").
func (q Quantity) String() string { return q.value.String() }

// IsZero reports whether q is the zero value, which is never a valid quantity.
func (q Quantity) IsZero() bool { return q.value.IsZero() }

// Decimal returns the value for arithmetic.
func (p Price) Decimal() decimal.Decimal { return p.value }

// String returns the canonical form: no exponent and no trailing zeros.
func (p Price) String() string { return p.value.String() }

// IsZero reports whether p is the zero value, which is never a valid price.
func (p Price) IsZero() bool { return p.value.IsZero() }

// Decimal returns the value for arithmetic.
func (f Fee) Decimal() decimal.Decimal { return f.value }

// String returns the canonical form: no exponent and no trailing zeros.
func (f Fee) String() string { return f.value.String() }

func parsePositive(field, s string) (decimal.Decimal, error) {
	d, err := parseDecimal(field, s)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if !d.IsPositive() {
		return decimal.Decimal{}, invalidInput(field, "must be greater than zero")
	}
	return d, nil
}

// parseDecimal accepts only ^[0-9]+(\.[0-9]+)?$ within numeric(28,10): no sign, exponent,
// whitespace, NaN or Infinity, at most 18 significant integer digits and 10 fraction digits.
func parseDecimal(field, s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Decimal{}, invalidInput(field, "is required")
	}
	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if !isDigits(intPart) || (hasDot && !isDigits(fracPart)) {
		return decimal.Decimal{}, invalidInput(field, "must be a plain decimal number such as 12.5")
	}
	if len(strings.TrimLeft(intPart, "0")) > maxIntegerDigits {
		return decimal.Decimal{}, invalidInput(field, "must have at most 18 digits before the decimal point")
	}
	if len(fracPart) > maxFractionDigits {
		return decimal.Decimal{}, invalidInput(field, "must have at most 10 digits after the decimal point")
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		// Unreachable after the checks above; never wrap err because it repeats the input.
		return decimal.Decimal{}, invalidInput(field, "must be a plain decimal number such as 12.5")
	}
	return d, nil
}
