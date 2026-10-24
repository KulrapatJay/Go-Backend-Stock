package domain_test

import (
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestParseSymbol(t *testing.T) {
	for in, want := range map[string]string{
		"AAPL": "AAPL", "aapl": "AAPL", " msft ": "MSFT", "brk.b": "BRK.B", "BF-B": "BF-B",
		"A": "A", "ABCDEFGHIJKLMNO": "ABCDEFGHIJKLMNO", // 15 characters
	} {
		s, err := domain.ParseSymbol(in)
		if err != nil || s.String() != want {
			t.Errorf("ParseSymbol(%q) = %q, %v; want %q", in, s, err, want)
		}
	}
	for _, in := range []string{
		"", "   ", "1ABC", ".ABC", "-ABC", "AB CD", "AB_C", "ABCDEFGHIJKLMNOP", // 16 characters
		"ıbm",  // dotless i upper-cases to ASCII "I"
		"ſpy",  // long s upper-cases to ASCII "S"
		"ＡＡＰＬ", // full-width letters
	} {
		_, err := domain.ParseSymbol(in)
		requireInvalidInput(t, err, "symbol")
	}
	var zero domain.Symbol
	if !zero.IsZero() {
		t.Error("zero Symbol.IsZero() = false")
	}
}

func TestParseEmail(t *testing.T) {
	for in, want := range map[string]string{
		"User@Example.COM":                        "user@example.com",
		"  a.b+tag@example.com  ":                 "a.b+tag@example.com", // dots and +suffix are kept
		"ÄLICE@EXAMPLE.COM":                       "älice@example.com",
		"O'Brien@Example.com":                     "o'brien@example.com",                     // an apostrophe is a legal local-part character
		strings.Repeat("a", 242) + "@example.com": strings.Repeat("a", 242) + "@example.com", // 254
	} {
		e, err := domain.ParseEmail(in)
		if err != nil || e.String() != want {
			t.Errorf("ParseEmail(%q) = %q, %v; want %q", in, e, err, want)
		}
	}
	for _, in := range []string{
		"", "   ", "user", "@example.com", "user@", "a@b@c", "us er@example.com", "user@exa\tmple.com",
		"user\x00@example.com", "\xff@example.com",
		"a,b@example.com", "a;b@example.com", "<a@example.com>", `"a"@example.com`, "a(b)@example.com",
		"a:b@example.com", "a[b]@example.com", `a\b@example.com`, "a@exa]mple.com",
		"a\u200bb@example.com",                    // a zero-width space hides a character
		"a\u202eb@example.com",                    // a bidi override reorders the text
		strings.Repeat("a", 243) + "@example.com", // 255
	} {
		_, err := domain.ParseEmail(in)
		requireInvalidInput(t, err, "email")
	}
}

func TestParseExecutedAt(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	for in, want := range map[string]time.Time{
		"2026-10-01T14:30:00Z":         time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC),
		"2026-10-01T21:30:00+07:00":    time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC),
		"2026-10-01T14:30:00.123456Z":  time.Date(2026, 10, 1, 14, 30, 0, 123456000, time.UTC),
		"2026-10-01T14:30:00.1234560Z": time.Date(2026, 10, 1, 14, 30, 0, 123456000, time.UTC), // trailing zero loses nothing
		"1970-01-01T00:00:00Z":         time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		"2026-10-16T12:00:00Z":         now, // exactly now is allowed
	} {
		got, err := domain.ParseExecutedAt(in, now)
		if err != nil || !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("ParseExecutedAt(%q) = %v, %v; want %v in UTC", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "2026-10-01", "2026-10-01T14:30:00", "2026-10-01 14:30:00Z", "1696170600",
		"2026-10-01T14:30:00.1234567Z", // sub-microsecond
		"1969-12-31T23:59:59.999999Z",  // before 1970
		"1970-01-01T06:59:59+07:00",    // before 1970 once converted to UTC
		"2026-10-16T12:00:00.000001Z",  // one microsecond in the future
		"2026-10-16T19:00:01+07:00",    // future once converted to UTC
	} {
		_, err := domain.ParseExecutedAt(in, now)
		requireInvalidInput(t, err, "executed_at")
	}
}

func TestParsePortfolioName(t *testing.T) {
	thai100 := strings.Repeat("ก", 100) // 300 bytes, 100 characters
	for in, want := range map[string]string{
		"Long term": "Long term", "  ระยะยาว  ": "ระยะยาว", thai100: thai100, "a": "a",
		"\tname\n": "name", "name\u0085": "name", // surrounding whitespace, including line breaks, is trimmed
	} {
		n, err := domain.ParsePortfolioName(in)
		if err != nil || n.String() != want {
			t.Errorf("ParsePortfolioName(%q) = %q, %v; want %q", in, n, err, want)
		}
	}
	for _, in := range []string{"", "   ", "\n\t", thai100 + "ก", "line\nbreak", "tab\there", "nul\x00", "c1\u0085x", "\xff"} {
		_, err := domain.ParsePortfolioName(in)
		requireInvalidInput(t, err, "name")
	}
	var zero domain.PortfolioName
	if !zero.IsZero() {
		t.Error("zero PortfolioName.IsZero() = false")
	}
}

func TestParsePortfolioDescription(t *testing.T) {
	thai1000 := strings.Repeat("ข", 1000)
	for _, in := range []string{
		"", "  kept as written  ", thai1000,
		"line\nbreak", "windows\r\nline", "ถือยาว 10 ปี\nเน้นปันผล\n", // line breaks are allowed (user decision 2026-10-17)
	} {
		d, err := domain.ParsePortfolioDescription(in)
		if err != nil || d.String() != in {
			t.Errorf("ParsePortfolioDescription(%q) = %q, %v; want unchanged", in, d, err)
		}
	}
	for _, in := range []string{
		thai1000 + "ข", "bell\a", "\xff", "tab\there", "lone\rcarriage", "ends with\r",
		"rtl\u202eoverride", "line\u2028separator", "para\u2029separator", "zero\u200bwidth", "bom\ufeff",
	} {
		_, err := domain.ParsePortfolioDescription(in)
		requireInvalidInput(t, err, "description")
	}
}

// TestPortfolioNameRejectsInvisibleAndDirectionChanges keeps names visibly distinct: invisible or
// direction-changing characters would let "Long term" and "Long term\u200b" look identical while
// passing the case-insensitive uniqueness check, or make a name that looks empty.
func TestPortfolioNameRejectsInvisibleAndDirectionChanges(t *testing.T) {
	for _, in := range []string{
		"\u200b", "\ufeff", "\u2060\u2060", "\u180e", "\u00ad", // invisible-only names
		"\u3164", "\u115f", "\uffa0", // Hangul fillers render as blanks
		"Long term\u200b", "Long\u00adterm", "Long\u3164term",
		"\u202eevil", "a\u061cb", "a\u200eb", "a\u2066b\u2069", // bidi controls
		"a\u2028b", "a\u2029b", // line and paragraph separators
	} {
		_, err := domain.ParsePortfolioName(in)
		requireInvalidInput(t, err, "name")
	}
	for _, in := range []string{
		"Long term", "ระยะยาว", "👨\u200d👩\u200d👧 family", // zero-width joiner inside emoji
		"می\u200cخواهم", // zero-width non-joiner is needed by Persian
		"📈", "株式",
	} {
		if _, err := domain.ParsePortfolioName(in); err != nil {
			t.Errorf("ParsePortfolioName(%q) error = %v", in, err)
		}
	}
}
