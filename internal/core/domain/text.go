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
