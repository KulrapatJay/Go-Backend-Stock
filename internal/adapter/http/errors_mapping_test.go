package http

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

func TestErrorHandlerMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		code       string
		message    string // "" = the domain message
		retryAfter string
		details    []string
	}{
		{"invalid input lists reasons", domain.NewReasonsError(domain.CodeInvalidInput, "password does not meet the requirements", "password_too_short", "password_missing_special"),
			400, "invalid_input", "", "", []string{"password_too_short", "password_missing_special"}},
		{"malformed json", domain.NewError(domain.CodeMalformedJSON, "request body is not valid"), 400, "malformed_json", "", "", nil},
		{"unauthenticated", domain.ErrUnauthenticated, 401, "unauthenticated", "", "", nil},
		{"invalid credentials", domain.ErrInvalidCredentials, 401, "invalid_credentials", "", "", nil},
		{"invalid verification", domain.ErrInvalidVerification, 400, "invalid_verification", "", "", nil},
		{"email not verified", domain.ErrEmailNotVerified, 403, "email_not_verified", "", "", nil},
		{"csrf failed", domain.ErrCSRFFailed, 403, "csrf_failed", "", "", nil},
		{"not found", domain.ErrNotFound, 404, "not_found", "", "", nil},
		{"identity conflict", domain.ErrIdentityConflict, 409, "identity_conflict", "", "", nil},
		{"rate limited rounds up", domain.NewRetryError(domain.CodeRateLimited, "too many requests", 1500*time.Millisecond), 429, "rate_limited", "", "2", nil},
		{"rate limited waits at least a second", domain.NewRetryError(domain.CodeRateLimited, "too many requests", 10*time.Millisecond), 429, "rate_limited", "", "1", nil},
		{"server busy", domain.NewError(domain.CodeServerBusy, "server is busy, try again shortly"), 503, "server_busy", "", "1", nil},
		{"dependency unavailable", domain.NewError(domain.CodeDependencyUnavailable, "session store is unavailable"), 503, "dependency_unavailable", "", "", nil},
		{"internal hides its message", domain.NewError(domain.CodeInternal, "stored password hash is malformed"), 500, "internal", "Internal server error", "", nil},
		{"audit write failure hides its cause", fmt.Errorf("%w: %w", domain.NewError(domain.CodeAuditWriteFailed, "could not record the audit event"), errors.New("connection reset")),
			500, "audit_write_failed", "Internal server error", "", nil},
		{"wrapped domain error", fmt.Errorf("login: %w", domain.ErrEmailNotVerified), 403, "email_not_verified", "", "", nil},
		{"unknown code is internal", domain.NewError("made_up", "secret detail"), 500, "internal", "Internal server error", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, logs := newTestLogger()
			app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(log)})
			app.Use(middleware.RequestID())
			app.Get("/x", func(fiber.Ctx) error { return tt.err })

			resp, body := doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/x", nil))
			got := decodeError(t, body)
			wantMessage := tt.message
			if wantMessage == "" {
				var de *domain.Error
				errors.As(tt.err, &de)
				wantMessage = de.Message
			}
			if resp.StatusCode != tt.status || got.Code != tt.code || got.Message != wantMessage {
				t.Errorf("got %d %s %q, want %d %s %q", resp.StatusCode, got.Code, got.Message, tt.status, tt.code, wantMessage)
			}
			if resp.Header.Get("Retry-After") != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", resp.Header.Get("Retry-After"), tt.retryAfter)
			}
			if !slices.Equal(got.Details, tt.details) {
				t.Errorf("details = %v, want %v", got.Details, tt.details)
			}
			if got.RequestID == "" || got.RequestID != resp.Header.Get(middleware.HeaderRequestID) {
				t.Errorf("request_id %q must match the X-Request-ID header", got.RequestID)
			}
			if tt.status >= 500 && !strings.Contains(logs.String(), `"event":"http.error"`) {
				t.Errorf("a 5xx must be logged server-side, log = %s", logs.String())
			}
		})
	}
}

func TestErrorBodyOmitsEmptyDetails(t *testing.T) {
	log, _ := newTestLogger()
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(log)})
	app.Get("/x", func(fiber.Ctx) error { return domain.ErrNotFound })
	_, body := doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/x", nil))
	if strings.Contains(string(body), "details") {
		t.Errorf("body %s must not carry an empty details field", body)
	}
}
