package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func TestErrorIsMatchesByCode(t *testing.T) {
	err := fmt.Errorf("load portfolio: %w", domain.NewError(domain.CodeNotFound, "portfolio not found"))

	if !errors.Is(err, domain.ErrNotFound) {
		t.Error("errors.Is(wrapped not_found, ErrNotFound) = false, want true")
	}
	if errors.Is(err, domain.ErrVersionConflict) {
		t.Error("errors.Is(not_found, ErrVersionConflict) = true, want false")
	}
	if errors.Is(err, errors.New("not_found: portfolio not found")) {
		t.Error("errors.Is matched a non-domain error with the same text")
	}
}

func TestErrorString(t *testing.T) {
	err := domain.NewError(domain.CodeInvalidInput, "quantity must be greater than zero")
	if got, want := err.Error(), "invalid_input: quantity must be greater than zero"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorCodesMatchAPIContract(t *testing.T) {
	// Spec section 11 lists these codes; HTTP adapters map them to statuses.
	want := map[domain.ErrorCode]string{
		domain.CodeInvalidInput: "invalid_input", domain.CodeMalformedJSON: "malformed_json",
		domain.CodeUnauthenticated: "unauthenticated", domain.CodeInvalidCredentials: "invalid_credentials",
		domain.CodeInvalidReset: "invalid_reset", domain.CodeInvalidVerification: "invalid_verification",
		domain.CodeCSRFFailed: "csrf_failed", domain.CodeAccessDenied: "access_denied",
		domain.CodeEmailNotVerified: "email_not_verified", domain.CodeVersionConflict: "version_conflict",
		domain.CodePreconditionRequired: "precondition_required", domain.CodeNotFound: "not_found",
		domain.CodeIdentityConflict: "identity_conflict", domain.CodeIdempotencyConflict: "idempotency_conflict",
		domain.CodePortfolioNotEmpty: "portfolio_not_empty", domain.CodePortfolioNameConflict: "portfolio_name_conflict",
		domain.CodeInsufficientShares: "insufficient_shares", domain.CodeInvalidHistory: "invalid_history",
		domain.CodeUnknownSymbol: "unknown_symbol", domain.CodeRateLimited: "rate_limited",
		domain.CodeDependencyUnavailable: "dependency_unavailable", domain.CodeFeatureDisabled: "feature_disabled",
		domain.CodeServerBusy: "server_busy", domain.CodeInternal: "internal",
		domain.CodeAuditWriteFailed: "audit_write_failed",
	}
	if len(want) != 25 {
		t.Fatalf("test lists %d codes, spec section 11 has 25", len(want))
	}
	for code, s := range want {
		if string(code) != s {
			t.Errorf("code %q, want %q", code, s)
		}
	}
}

func TestErrorIsWithNilTargetDoesNotPanic(t *testing.T) {
	var nilErr *domain.Error
	err := fmt.Errorf("load: %w", domain.ErrNotFound)
	if errors.Is(err, nilErr) {
		t.Error("errors.Is(not_found, (*domain.Error)(nil)) = true, want false")
	}
}
