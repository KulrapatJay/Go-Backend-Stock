package domain_test

import (
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

// echoMarker never appears in a fixed error message, so finding it means the input leaked.
const echoMarker = "ZQXJ"

// TestErrorsNeverEchoInput feeds every parser an invalid value carrying echoMarker and checks
// the message does not contain it: messages reach clients and logs.
func TestErrorsNeverEchoInput(t *testing.T) {
	parsers := map[string]func(string) error{
		"ParseID":       func(s string) error { _, err := domain.ParseID("id", s); return err },
		"ParsePage":     func(s string) error { _, err := domain.ParsePage(s, ""); return err },
		"ParseQuantity": func(s string) error { _, err := domain.ParseQuantity(s); return err },
		"ParsePrice":    func(s string) error { _, err := domain.ParsePrice(s); return err },
		"ParseFee":      func(s string) error { _, err := domain.ParseFee(s); return err },
	}
	for name, parse := range parsers {
		for _, in := range []string{echoMarker, "1" + echoMarker, echoMarker + "@", strings.Repeat(echoMarker, 300)} {
			err := parse(in)
			if err == nil {
				t.Fatalf("%s(%q) accepted an invalid value", name, in)
			}
			if strings.Contains(strings.ToUpper(err.Error()), echoMarker) {
				t.Errorf("%s error %q echoes the input", name, err)
			}
		}
	}
}
