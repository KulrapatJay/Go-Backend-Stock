package domain_test

import (
	"net/netip"
	"strings"
	"testing"
	"unicode/utf8"

	"go-backend-stock/internal/core/domain"
)

func TestMaskIP(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.77":           "203.0.113.0/24",
		"::ffff:203.0.113.77":    "203.0.113.0/24", // an IPv4 address in IPv6 notation is still IPv4
		"2001:db8:1234:5678::1":  "2001:db8:1234::/48",
		"fe80::1%eth0":           "fe80::/48", // the zone is dropped
		"2001:db8:ffff:ffff::99": "2001:db8:ffff::/48",
	} {
		if got := domain.MaskIP(netip.MustParseAddr(in)); got != want {
			t.Errorf("MaskIP(%s) = %q, want %q", in, got, want)
		}
	}
	if got := domain.MaskIP(netip.Addr{}); got != "" {
		t.Errorf("MaskIP(zero) = %q, want empty", got)
	}
}

func TestSanitizeUserAgent(t *testing.T) {
	if got := domain.SanitizeUserAgent("Mozilla/5.0 (X11; Linux x86_64)"); got != "Mozilla/5.0 (X11; Linux x86_64)" {
		t.Errorf("a normal user agent changed to %q", got)
	}
	if got := domain.SanitizeUserAgent(""); got != "" {
		t.Errorf("empty user agent = %q", got)
	}

	for name, in := range map[string]string{
		"line break":    "Mozilla/5.0\r\nX-Injected: yes",
		"nul":           "a\x00b",
		"escape":        "a\x1b[31mb",
		"c1 control":    "a\u0085b",
		"bidi override": "a‮b",
		"zero width":    "a​b",
	} {
		got := domain.SanitizeUserAgent(in)
		if strings.ContainsAny(got, "\r\n\x00\x1b\u0085‮​") {
			t.Errorf("%s: SanitizeUserAgent(%q) = %q still has the character", name, in, got)
		}
		if got == "" {
			t.Errorf("%s: the user agent should keep its other characters", name)
		}
	}

	if got := domain.SanitizeUserAgent("a\xffb"); !utf8.ValidString(got) {
		t.Errorf("invalid UTF-8 survived: %q", got)
	}
}

func TestSanitizeUserAgentCapsLengthOnARuneBoundary(t *testing.T) {
	if got := domain.SanitizeUserAgent(strings.Repeat("a", 1000)); len(got) != domain.MaxUserAgentBytes {
		t.Errorf("len = %d, want %d", len(got), domain.MaxUserAgentBytes)
	}
	// 200 two-byte runes = 400 bytes: the cut at byte 256 falls between runes.
	if got := domain.SanitizeUserAgent(strings.Repeat("é", 200)); len(got) != 256 || !utf8.ValidString(got) {
		t.Errorf("len = %d valid = %v, want 256 bytes of valid UTF-8", len(got), utf8.ValidString(got))
	}
	// One leading byte moves every rune start to an odd offset, so byte 256 is mid-rune.
	if got := domain.SanitizeUserAgent("a" + strings.Repeat("é", 200)); len(got) != 255 || !utf8.ValidString(got) {
		t.Errorf("len = %d valid = %v, want 255 bytes of valid UTF-8", len(got), utf8.ValidString(got))
	}
}
