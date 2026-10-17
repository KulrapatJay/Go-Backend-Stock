package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// hasControl reports whether s contains a C0 control character, DEL or a C1 control character.
func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// hasControlExceptLineBreaks is hasControl that allows "\n" and the "\r\n" pair, for multi-line text.
func hasControlExceptLineBreaks(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\n':
		case c == '\r' && i+1 < len(s) && s[i+1] == '\n':
		case c < 0x20 || c == 0x7f:
			return true
		}
	}
	// C1 controls (U+0080-U+009F) are multi-byte in UTF-8, so check them as runes.
	return strings.IndexFunc(s, func(r rune) bool { return r >= 0x80 && r < 0xa0 }) >= 0
}

const (
	zeroWidthNonJoiner = '‌'
	zeroWidthJoiner    = '‍'
)

// isHidden reports characters that hide or reorder text: format characters such as zero-width
// spaces, soft hyphens, the BOM and bidi controls (except the two joiners that emoji, Persian and
// Indic scripts need), line and paragraph separators, and Hangul fillers, which render as blanks.
// They would let two names look identical while passing the uniqueness check.
func isHidden(r rune) bool {
	switch {
	case r == zeroWidthNonJoiner || r == zeroWidthJoiner:
		return false
	case unicode.Is(unicode.Cf, r), unicode.In(r, unicode.Zl, unicode.Zp):
		return true
	}
	return r == 0x115f || r == 0x1160 || r == 0x3164 || r == 0xffa0
}

func hasHidden(s string) bool { return strings.IndexFunc(s, isHidden) >= 0 }

// hasVisible reports whether s has at least one character that renders as a mark.
func hasVisible(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsGraphic(r) && !unicode.IsSpace(r) }) >= 0
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// isText reports whether s is valid UTF-8 of min..max runes without control characters.
func isText(s string, minRunes, maxRunes int) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && n >= minRunes && n <= maxRunes && !hasControl(s)
}
