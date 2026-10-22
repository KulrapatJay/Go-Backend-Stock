package domain

import (
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxUserAgentBytes is the longest user agent kept in audit metadata.
const MaxUserAgentBytes = 256

// MaskIP returns the network of addr for audit metadata: /24 for IPv4 and /48 for IPv6, such as
// "203.0.113.0/24". It returns "" for the zero Addr. Rate limiting uses the full address instead.
func MaskIP(addr netip.Addr) string {
	if !addr.IsValid() {
		return ""
	}
	addr = addr.Unmap().WithZone("")
	bits := 48
	if addr.Is4() {
		bits = 24
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return ""
	}
	return prefix.String()
}

// SanitizeUserAgent makes a User-Agent header safe to store and log: invalid UTF-8, control
// characters and invisible or bidi characters become a space, and the result is cut to
// MaxUserAgentBytes on a rune boundary.
func SanitizeUserAgent(s string) string {
	s = strings.ToValidUTF8(s, " ")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || isHidden(r) {
			return ' '
		}
		return r
	}, s)
	if len(s) <= MaxUserAgentBytes {
		return s
	}
	cut := MaxUserAgentBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
