package domain_test

import (
	"errors"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func TestNewReasonsErrorCopiesReasons(t *testing.T) {
	in := []string{"a", "b"}
	err := domain.NewReasonsError(domain.CodeInvalidInput, "x is wrong", in...)
	in[0] = "changed"

	if err.Reasons[0] != "a" || len(err.Reasons) != 2 {
		t.Errorf("Reasons = %v, want a copy of [a b]", err.Reasons)
	}
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Error("an error with reasons must still match its sentinel by code")
	}
	if plain := domain.NewError(domain.CodeInvalidInput, "x"); plain.Reasons != nil {
		t.Errorf("NewError Reasons = %v, want nil", plain.Reasons)
	}
}
