package domain_test

import (
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

var (
	max18Integer  = strings.Repeat("9", 18)
	max10Fraction = "0." + strings.Repeat("9", 10)
)

func TestParseDecimalsAccept(t *testing.T) {
	tests := []struct{ in, want string }{
		{"1", "1"},
		{"0.5", "0.5"},
		{"0.50", "0.5"},
		{"007", "7"},
		{"7.0", "7"},
		{"200.15", "200.15"},
		{"0.0000000001", "0.0000000001"},
		{max18Integer, max18Integer},
		{max10Fraction, max10Fraction},
		{max18Integer + "." + strings.Repeat("9", 10), max18Integer + "." + strings.Repeat("9", 10)},
		{"000" + max18Integer, max18Integer}, // leading zeros are not significant digits
	}
	for _, tt := range tests {
		q, err := domain.ParseQuantity(tt.in)
		if err != nil || q.String() != tt.want {
			t.Errorf("ParseQuantity(%q) = %q, %v; want %q", tt.in, q.String(), err, tt.want)
		}
		p, err := domain.ParsePrice(tt.in)
		if err != nil || p.String() != tt.want {
			t.Errorf("ParsePrice(%q) = %q, %v; want %q", tt.in, p.String(), err, tt.want)
		}
		f, err := domain.ParseFee(tt.in)
		if err != nil || f.String() != tt.want {
			t.Errorf("ParseFee(%q) = %q, %v; want %q", tt.in, f.String(), err, tt.want)
		}
	}
}

func TestParseDecimalsRejectBadFormat(t *testing.T) {
	bad := []string{
		"", " ", " 1", "1 ", "+1", "-1", "-0", ".5", "5.", "1.2.3", "1,000", "1_000",
		"1e3", "1E3", "0x10", "NaN", "nan", "Infinity", "-Infinity", "Inf",
		"١", // Arabic-Indic digit one: not ASCII
	}
	for _, s := range bad {
		_, err := domain.ParseQuantity(s)
		requireInvalidInput(t, err, "quantity")
		_, err = domain.ParsePrice(s)
		requireInvalidInput(t, err, "price")
		_, err = domain.ParseFee(s)
		requireInvalidInput(t, err, "fee")
	}
}

func TestParseDecimalsRejectOutOfRange(t *testing.T) {
	tooLong := []string{
		"1" + max18Integer,             // 19 integer digits
		"0." + strings.Repeat("1", 11), // 11 fraction digits
		"1." + strings.Repeat("0", 11), // 11 fraction digits even though they are zeros
		"1" + max18Integer + "." + "0", // 19 integer digits with a fraction
	}
	for _, s := range tooLong {
		_, err := domain.ParseQuantity(s)
		requireInvalidInput(t, err, "quantity")
		_, err = domain.ParseFee(s)
		requireInvalidInput(t, err, "fee")
	}
}

func TestZeroIsOnlyValidForFee(t *testing.T) {
	for _, s := range []string{"0", "0.0", "000", "0.0000000000"} {
		_, err := domain.ParseQuantity(s)
		requireInvalidInput(t, err, "quantity")
		_, err = domain.ParsePrice(s)
		requireInvalidInput(t, err, "price")
		f, err := domain.ParseFee(s)
		if err != nil || f.String() != "0" {
			t.Errorf("ParseFee(%q) = %q, %v; want \"0\"", s, f.String(), err)
		}
	}
}

func TestDecimalZeroValues(t *testing.T) {
	var q domain.Quantity
	var p domain.Price
	var f domain.Fee
	if !q.IsZero() || !p.IsZero() {
		t.Error("zero Quantity/Price must report IsZero")
	}
	if f.String() != "0" || !f.Decimal().IsZero() {
		t.Errorf("zero Fee = %q, want a valid fee of 0", f.String())
	}
	if q2, _ := domain.ParseQuantity("0.1"); q2.IsZero() {
		t.Error("parsed quantity reports IsZero")
	}
}

func TestDecimalArithmeticKeepsPrecision(t *testing.T) {
	q, _ := domain.ParseQuantity("0.1")
	p, _ := domain.ParsePrice("0.2")
	if got := q.Decimal().Add(p.Decimal()).String(); got != "0.3" {
		t.Errorf("0.1 + 0.2 = %s, want exactly 0.3 (no float)", got)
	}
}

// FuzzParseQuantity checks that no input panics and that every accepted value survives a
// round trip through its canonical string.
func FuzzParseQuantity(f *testing.F) {
	for _, seed := range []string{"1", "0.5", "007", "1e3", "-1", "", ".5", max18Integer, max10Fraction, "0.0000000001"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, err := domain.ParseQuantity(s)
		if err != nil {
			return
		}
		again, err := domain.ParseQuantity(q.String())
		if err != nil {
			t.Fatalf("canonical %q of %q does not parse: %v", q.String(), s, err)
		}
		if !again.Decimal().Equal(q.Decimal()) || again.String() != q.String() {
			t.Fatalf("round trip of %q: %q != %q", s, again.String(), q.String())
		}
	})
}
