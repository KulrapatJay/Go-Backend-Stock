package service

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

const (
	blocklistMinLength = 6
	blocklistMaxLength = 32
)

// FilterBlocklistEntry normalises one line of a list of common passwords. It returns the entry
// in lower case and true only if some password that passes our other rules (6-32 printable
// ASCII characters without spaces, with a lower-case letter, an upper-case letter and a special
// character) could equal it once lower-cased, which needs a letter and a special character. Every
// other line can never match, so leaving it out gives the same result with a smaller file.
func FilterBlocklistEntry(line string) (string, bool) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) < blocklistMinLength || len(line) > blocklistMaxLength {
		return "", false
	}
	var hasLetter, hasSpecial bool
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c < 0x21 || c > 0x7e:
			return "", false
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= '0' && c <= '9':
		default:
			hasSpecial = true
		}
	}
	if !hasLetter || !hasSpecial {
		return "", false
	}
	return strings.ToLower(line), true
}

// BuildBlocklist reads a source list, keeps the entries FilterBlocklistEntry accepts, removes
// duplicates and writes them sorted, one per line. It returns how many it wrote.
func BuildBlocklist(src io.Reader, dst io.Writer) (int, error) {
	kept := make(map[string]struct{})
	sc := newLineScanner(src)
	for sc.Scan() {
		if entry, ok := FilterBlocklistEntry(sc.Text()); ok {
			kept[entry] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("read source list: %w", err)
	}

	w := bufio.NewWriter(dst)
	entries := slices.Sorted(maps.Keys(kept))
	for _, entry := range entries {
		if _, err := w.WriteString(entry + "\n"); err != nil {
			return 0, fmt.Errorf("write blocklist: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return 0, fmt.Errorf("write blocklist: %w", err)
	}
	return len(entries), nil
}

// Blocklist is a set of lower-case common passwords, loaded once at startup.
type Blocklist struct{ entries map[string]struct{} }

// NewBlocklist reads one entry per line. An empty list is an error because it means the bundled
// file is missing or broken, and a password check that matches nothing looks like a working one.
func NewBlocklist(r io.Reader) (*Blocklist, error) {
	list := &Blocklist{entries: make(map[string]struct{})}
	sc := newLineScanner(r)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			list.entries[strings.ToLower(line)] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read blocklist: %w", err)
	}
	if len(list.entries) == 0 {
		return nil, errors.New("blocklist is empty")
	}
	return list, nil
}

// Contains reports whether lowercase is on the list. Callers pass the password already lower-cased.
func (b *Blocklist) Contains(lowercase string) bool {
	_, ok := b.entries[lowercase]
	return ok
}

// Len returns the number of entries.
func (b *Blocklist) Len() int { return len(b.entries) }

func newLineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return sc
}
