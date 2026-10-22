package service

import (
	"bytes"
	"strings"
	"testing"
)

func TestFilterBlocklistEntry(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"P@ssw0rd", "p@ssw0rd", true},
		{"ABCDEF!", "abcdef!", true},
		{"abc!12\r\n", "abc!12", true},
		{strings.Repeat("a", 31) + "!", strings.Repeat("a", 31) + "!", true}, // 32 bytes
		{"password", "", false},                                              // no special character, so no password that passes our rules can equal it
		{"123456!", "", false},                                               // no letter
		{"a!", "", false},                                                    // shorter than 6
		{strings.Repeat("a", 32) + "!", "", false},                           // 33 bytes
		{"pass word!", "", false},                                            // space
		{"pässwörd!", "", false},                                             // non-ASCII
		{"รหัสผ่าน!", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := FilterBlocklistEntry(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("FilterBlocklistEntry(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestBuildBlocklist(t *testing.T) {
	src := "P@ssw0rd\npassword\nP@SSW0RD\n123456\nqwerty!\nabc!12\r\nabc!12\nzzz!zzz\n"
	var out bytes.Buffer

	n, err := BuildBlocklist(strings.NewReader(src), &out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "abc!12\np@ssw0rd\nqwerty!\nzzz!zzz\n"; out.String() != want || n != 4 {
		t.Errorf("BuildBlocklist() = %d entries %q, want 4 entries %q", n, out.String(), want)
	}
}

func TestNewBlocklist(t *testing.T) {
	list, err := NewBlocklist(strings.NewReader("p@ssw0rd\n\nqwerty!\n"))
	if err != nil {
		t.Fatal(err)
	}
	if list.Len() != 2 || !list.Contains("p@ssw0rd") || !list.Contains("qwerty!") {
		t.Errorf("Len = %d, want both entries and nothing else", list.Len())
	}
	if list.Contains("P@ssw0rd") || list.Contains("other!1") {
		t.Error("Contains takes a lower-case password and matches exactly")
	}

	if _, err := NewBlocklist(strings.NewReader("\n\n")); err == nil {
		t.Error("an empty blocklist must be an error: it means the embedded file is broken")
	}
}

func TestBuiltBlocklistLoads(t *testing.T) {
	var built bytes.Buffer
	if _, err := BuildBlocklist(strings.NewReader("Hello!World\nnope\n"), &built); err != nil {
		t.Fatal(err)
	}
	list, err := NewBlocklist(&built)
	if err != nil || !list.Contains("hello!world") || list.Len() != 1 {
		t.Errorf("round trip: Len = %d, error = %v", list.Len(), err)
	}
}
