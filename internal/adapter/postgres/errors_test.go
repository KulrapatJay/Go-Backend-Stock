package postgres

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go-backend-stock/internal/core/domain"
)

func bigInt(t *testing.T, s string) *big.Int {
	t.Helper()
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad big.Int %q", s)
	}
	return n
}

func TestMapError(t *testing.T) {
	unique := func(constraint string) error {
		return fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505", ConstraintName: constraint,
			Message: `duplicate key value violates unique constraint "` + constraint + `"`, Detail: "Key (email)=(secret@example.com) already exists."})
	}
	tests := []struct {
		name string
		in   error
		want *domain.Error
	}{
		{"no rows", fmt.Errorf("get: %w", pgx.ErrNoRows), domain.ErrNotFound},
		{"portfolio name", unique("portfolios_user_name_unique"), domain.ErrPortfolioNameConflict},
		{"google subject", unique("auth_providers_subject_key"), domain.ErrIdentityConflict},
		{"one google per user", unique("auth_providers_user_provider_key"), domain.ErrIdentityConflict},
		{"email race", unique("users_email_key"), domain.ErrIdentityConflict},
		{"idempotency key race", unique("idempotency_keys_pkey"), domain.ErrIdempotencyConflict},
		{"portfolio still has trades", &pgconn.PgError{Code: "23503", ConstraintName: "transactions_portfolio_id_fkey"}, domain.ErrPortfolioNotEmpty},
		{"other unique", unique("sessions_token_hash_key"), domain.ErrInternal},
		{"other foreign key", &pgconn.PgError{Code: "23503", ConstraintName: "idempotency_keys_portfolio_id_fkey"}, domain.ErrInternal},
		{"check violation", &pgconn.PgError{Code: "23514", ConstraintName: "audit_events_metadata_object"}, domain.ErrInternal},
		{"connection error", errors.New("dial tcp 127.0.0.1:5432: connection refused"), domain.ErrInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.in)
			if !errors.Is(got, tt.want) {
				t.Fatalf("mapError() = %v, want %s", got, tt.want.Code)
			}
			var de *domain.Error
			if errors.As(got, &de) && strings.Contains(de.Message, "secret@example.com") {
				t.Errorf("domain message leaks row values: %q", de.Message)
			}
		})
	}
	if mapError(nil) != nil {
		t.Error("mapError(nil) must be nil")
	}
	// Internal errors keep the database error for server-side logs.
	raw := &pgconn.PgError{Code: "23514", ConstraintName: "x"}
	if got := mapError(raw); !errors.As(got, new(*pgconn.PgError)) {
		t.Errorf("internal error %v lost the original database error", got)
	}
}
