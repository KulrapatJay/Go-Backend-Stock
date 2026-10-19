package postgres

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"go-backend-stock/internal/core/domain"
)

// Mapping between sqlc's pgtype values and domain types. Values read from the database go back
// through the domain Parse* functions (ADR-0013); a stored value that fails them is a bug or
// corrupt data, so fromStored turns the error into internal rather than invalid_input.

func pgUUID(id domain.ID) pgtype.UUID {
	if id.IsZero() {
		return pgtype.UUID{} // NULL
	}
	return pgtype.UUID{Bytes: uuid.MustParse(id.String()), Valid: true}
}

// idFromPG maps a NOT NULL uuid column.
func idFromPG(field string, u pgtype.UUID) (domain.ID, error) {
	if !u.Valid {
		return domain.ID{}, domain.NewError(domain.CodeInternal, "stored "+field+" is NULL")
	}
	id, err := domain.ParseID(field, uuid.UUID(u.Bytes).String())
	return id, fromStored(err)
}

// optionalIDFromPG maps a nullable uuid column; NULL becomes the zero ID.
func optionalIDFromPG(field string, u pgtype.UUID) (domain.ID, error) {
	if !u.Valid {
		return domain.ID{}, nil
	}
	return idFromPG(field, u)
}

func pgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func pgOptionalTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{} // NULL
	}
	return pgTime(*t)
}

// timeFromPG maps a timestamptz column to UTC; NULL becomes the zero time.
func timeFromPG(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// optionalTimeFromPG maps a nullable timestamptz column; NULL becomes nil.
func optionalTimeFromPG(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// pgText maps an optional string: "" is stored as NULL.
func pgText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// pgNumeric maps a canonical decimal string (from Quantity/Price/Fee.String()) to numeric.
func pgNumeric(s string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		return pgtype.Numeric{}, domain.NewError(domain.CodeInternal, "numeric value cannot be encoded")
	}
	return n, nil
}

// numericString returns the exact decimal string of a numeric column without going through
// float. NULL, NaN and infinities are errors: the schema forbids them.
func numericString(field string, n pgtype.Numeric) (string, error) {
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil {
		return "", domain.NewError(domain.CodeInternal, "stored "+field+" is not a finite number")
	}
	return decimal.NewFromBigInt(n.Int, n.Exp).String(), nil
}

// fromStored converts a domain invalid_input error about a stored value into internal: the client
// did not send that value, so it must not see a 400 for it.
func fromStored(err error) error {
	var de *domain.Error
	if errors.As(err, &de) && de.Code == domain.CodeInvalidInput {
		return domain.NewError(domain.CodeInternal, "stored "+de.Message)
	}
	return err
}
