package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"go-backend-stock/internal/core/domain"
)

func TestNumericRoundTripIsExact(t *testing.T) {
	for _, s := range []string{"0.0000000001", "1", "0.5", "123456789012345678.123456789", "999999999999999999.9999999999"} {
		n, err := pgNumeric(s)
		if err != nil {
			t.Fatalf("pgNumeric(%q) error = %v", s, err)
		}
		got, err := numericString("quantity", n)
		if err != nil || got != s {
			t.Errorf("round trip of %q = %q, %v", s, got, err)
		}
	}
	// Trailing zeros from numeric(28,10) disappear in the canonical form.
	n := pgtype.Numeric{Int: bigInt(t, "15000000000"), Exp: -10, Valid: true}
	if got, _ := numericString("price", n); got != "1.5" {
		t.Errorf("numericString(1.5000000000) = %q, want 1.5", got)
	}
}

func TestNumericStringRejectsNonFinite(t *testing.T) {
	for name, n := range map[string]pgtype.Numeric{
		"NULL":      {},
		"NaN":       {NaN: true, Valid: true},
		"+Infinity": {InfinityModifier: pgtype.Infinity, Valid: true},
		"-Infinity": {InfinityModifier: pgtype.NegativeInfinity, Valid: true},
	} {
		_, err := numericString("fee", n)
		if !errors.Is(err, domain.ErrInternal) {
			t.Errorf("%s: error = %v, want internal", name, err)
		}
	}
}

func TestIDMapping(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	back, err := idFromPG("id", pgUUID(id))
	if err != nil || back != id {
		t.Fatalf("round trip = %v, %v; want %v", back, err, id)
	}
	if pgUUID(domain.ID{}).Valid {
		t.Error("zero ID must map to NULL")
	}
	if got, err := optionalIDFromPG("actor_user_id", pgtype.UUID{}); err != nil || !got.IsZero() {
		t.Errorf("NULL optional ID = %v, %v; want zero", got, err)
	}
	if _, err := idFromPG("id", pgtype.UUID{}); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("NULL required ID error = %v, want internal", err)
	}
	// The nil UUID is never a valid ID; reading one is an internal error, not invalid_input.
	if _, err := idFromPG("id", pgtype.UUID{Valid: true}); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("nil UUID error = %v, want internal", err)
	}
}

func TestTimeMappingIsUTC(t *testing.T) {
	bangkok := time.FixedZone("ICT", 7*60*60)
	in := time.Date(2026, 10, 17, 21, 0, 0, 123000, bangkok)
	got := timeFromPG(pgTime(in))
	if !got.Equal(in) || got.Location() != time.UTC {
		t.Errorf("timeFromPG = %v, want %v in UTC", got, in)
	}
	if optionalTimeFromPG(pgtype.Timestamptz{}) != nil || pgOptionalTime(nil).Valid {
		t.Error("NULL timestamps must map to nil and back")
	}
}

func TestFromStoredTurnsInvalidInputIntoInternal(t *testing.T) {
	_, parseErr := domain.ParseSymbol("not a symbol")
	err := fromStored(parseErr)
	if !errors.Is(err, domain.ErrInternal) || errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("fromStored(invalid_input) = %v, want internal only", err)
	}
	if fromStored(nil) != nil {
		t.Error("fromStored(nil) must be nil")
	}
	if got := fromStored(domain.ErrNotFound); !errors.Is(got, domain.ErrNotFound) {
		t.Errorf("fromStored(not_found) = %v, want it unchanged", got)
	}
}

func TestTextMappingStoresEmptyAsNull(t *testing.T) {
	if pgText("").Valid {
		t.Error(`pgText("") must be NULL`)
	}
	if got := pgText("invalid_credentials"); !got.Valid || got.String != "invalid_credentials" {
		t.Errorf("pgText = %+v, want a valid text", got)
	}
}
