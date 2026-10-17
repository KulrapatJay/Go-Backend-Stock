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
