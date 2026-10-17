package domain_test

import (
	"math"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func TestParseVersion(t *testing.T) {
	for _, n := range []int64{1, 2, math.MaxInt32} {
		v, err := domain.ParseVersion(n)
		if err != nil || int64(v.Int32()) != n || v.IsZero() {
			t.Errorf("ParseVersion(%d) = %v, %v", n, v.Int32(), err)
		}
	}
	for _, n := range []int64{0, -1, math.MaxInt32 + 1} {
		_, err := domain.ParseVersion(n)
		requireInvalidInput(t, err, "version")
	}
	var zero domain.Version
	if !zero.IsZero() {
		t.Error("zero Version.IsZero() = false")
	}
}

func TestParsePage(t *testing.T) {
	p, err := domain.ParsePage("", "")
	if err != nil || p.Limit() != domain.DefaultPageLimit || p.Cursor() != "" || p.IsZero() {
		t.Fatalf("ParsePage(\"\", \"\") = limit %d cursor %q err %v, want default 20", p.Limit(), p.Cursor(), err)
	}
	for raw, want := range map[string]int{"1": 1, "100": 100, "007": 7} {
		p, err := domain.ParsePage(raw, "abc")
		if err != nil || p.Limit() != want || p.Cursor() != "abc" {
			t.Errorf("ParsePage(%q) = %d, %v, want %d", raw, p.Limit(), err, want)
		}
	}
	for _, raw := range []string{"0", "101", "-1", "+5", "1.5", "abc", " 5", "1000000000000000000000"} {
		_, err := domain.ParsePage(raw, "")
		requireInvalidInput(t, err, "limit")
	}
	if _, err := domain.ParsePage("", strings.Repeat("a", domain.MaxCursorBytes)); err != nil {
		t.Errorf("1024-byte cursor rejected: %v", err)
	}
	_, err = domain.ParsePage("", strings.Repeat("a", domain.MaxCursorBytes+1))
	requireInvalidInput(t, err, "cursor")

	var zero domain.Page
	if !zero.IsZero() {
		t.Error("zero Page.IsZero() = false")
	}
}
