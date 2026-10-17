package domain_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"go-backend-stock/internal/core/domain"
)

func TestNewIDIsUUIDv7(t *testing.T) {
	id := mustID(t)
	u, err := uuid.Parse(id.String())
	if err != nil || u.Version() != 7 {
		t.Fatalf("NewID() = %q, want a UUIDv7 (err %v)", id, err)
	}
	if id.IsZero() {
		t.Error("NewID().IsZero() = true")
	}
	if mustID(t) == id {
		t.Error("two NewID() calls returned the same ID")
	}
}

func TestParseID(t *testing.T) {
	const canonical = "0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"

	id, err := domain.ParseID("portfolio_id", strings.ToUpper(canonical))
	if err != nil {
		t.Fatalf("ParseID(upper case) error = %v", err)
	}
	if id.String() != canonical {
		t.Errorf("String() = %q, want lowercase %q", id.String(), canonical)
	}

	for _, bad := range []string{
		"",
		"not-a-uuid",
		"00000000-0000-0000-0000-000000000000", // nil UUID
		"0192f0c17a8e7cc49d2b3f6c1a2b4c5d",     // no hyphens
		"{0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d}",
		"urn:uuid:0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d",
		" " + canonical,
	} {
		_, err := domain.ParseID("portfolio_id", bad)
		requireInvalidInput(t, err, "portfolio_id")
	}
}

func TestZeroIDIsEmpty(t *testing.T) {
	var id domain.ID
	if !id.IsZero() || id.String() != "" {
		t.Errorf("zero ID: IsZero=%v String=%q, want true and empty", id.IsZero(), id.String())
	}
}
