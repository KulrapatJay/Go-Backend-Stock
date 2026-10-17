package domain_test

import (
	"errors"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

// requireCode fails unless err is a *domain.Error with the wanted code, and returns its message.
func requireCode(t *testing.T, err error, want domain.ErrorCode) string {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want *domain.Error with code %s", err, err, want)
	}
	if de.Code != want {
		t.Fatalf("code = %s, want %s (message %q)", de.Code, want, de.Message)
	}
	return de.Message
}

// requireInvalidInput fails unless err is invalid_input and its message starts with field.
// Whether messages echo input is checked separately with a marker (TestErrorsNeverEchoInput),
// because short inputs such as "1" legitimately appear in fixed messages.
func requireInvalidInput(t *testing.T, err error, field string) {
	t.Helper()
	msg := requireCode(t, err, domain.CodeInvalidInput)
	if !strings.HasPrefix(msg, field+" ") {
		t.Errorf("message %q does not start with field %q", msg, field)
	}
}

func mustID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	return id
}
