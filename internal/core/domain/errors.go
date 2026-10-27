// Package domain holds the core's entities, value types and errors. It depends only on the
// standard library, github.com/shopspring/decimal and github.com/google/uuid.
//
// Values that come from outside the core are converted with Parse* functions, which return
// invalid_input errors. New* constructors check values the server builds itself and return
// internal errors, because a violation there is a bug rather than bad user input.
package domain

import "time"

// ErrorCode is a stable machine-readable code shared with the API contract (spec section 11).
type ErrorCode string

const (
	CodeInvalidInput          ErrorCode = "invalid_input"
	CodeMalformedJSON         ErrorCode = "malformed_json"
	CodeUnauthenticated       ErrorCode = "unauthenticated"
	CodeInvalidCredentials    ErrorCode = "invalid_credentials" //nolint:gosec // G101 false positive: an error code name, not a credential.
	CodeInvalidReset          ErrorCode = "invalid_reset"
	CodeInvalidVerification   ErrorCode = "invalid_verification"
	CodeCSRFFailed            ErrorCode = "csrf_failed"
	CodeAccessDenied          ErrorCode = "access_denied"
	CodeEmailNotVerified      ErrorCode = "email_not_verified"
	CodeVersionConflict       ErrorCode = "version_conflict"
	CodePreconditionRequired  ErrorCode = "precondition_required"
	CodeNotFound              ErrorCode = "not_found"
	CodeIdentityConflict      ErrorCode = "identity_conflict"
	CodeIdempotencyConflict   ErrorCode = "idempotency_conflict"
	CodePortfolioNotEmpty     ErrorCode = "portfolio_not_empty"
	CodePortfolioNameConflict ErrorCode = "portfolio_name_conflict"
	CodeInsufficientShares    ErrorCode = "insufficient_shares"
	CodeInvalidHistory        ErrorCode = "invalid_history"
	CodeUnknownSymbol         ErrorCode = "unknown_symbol"
	CodeRateLimited           ErrorCode = "rate_limited"
	CodeDependencyUnavailable ErrorCode = "dependency_unavailable"
	CodeFeatureDisabled       ErrorCode = "feature_disabled"
	CodeServerBusy            ErrorCode = "server_busy"
	CodeInternal              ErrorCode = "internal"
	CodeAuditWriteFailed      ErrorCode = "audit_write_failed"

	// CodeIdentityRejected is raised by the identity adapter when the provider or the ID token check
	// refuses a sign-in. The Google callback turns it into an audited "failed" outcome. It is not
	// in domainStatus, so it never reaches a client: the HTTP adapter answers with a generic 500.
	CodeIdentityRejected ErrorCode = "identity_rejected"
)

// Error is the single domain error type. Message is safe to return to clients: it names the
// field and the rule, and never contains the value the client sent.
type Error struct {
	Code    ErrorCode
	Message string
	// Reasons are stable codes that say which rules a value broke, such as "password_too_short",
	// so a client can point at the exact problem. Optional; the HTTP adapter returns them as
	// error.details.
	Reasons []string
	// RetryAfter is how long the client should wait before trying again. It is set on rate_limited
	// errors; the HTTP adapter sends it as the Retry-After header.
	RetryAfter time.Duration
}

// NewError returns an *Error with the given code and client-safe message.
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

// NewReasonsError returns an *Error that also lists the rule codes the value broke. It copies
// reasons so the caller may reuse the slice.
func NewReasonsError(code ErrorCode, message string, reasons ...string) *Error {
	return &Error{Code: code, Message: message, Reasons: append([]string(nil), reasons...)}
}

// NewRetryError returns an *Error that tells the client how long to wait, such as rate_limited.
func NewRetryError(code ErrorCode, message string, retryAfter time.Duration) *Error {
	return &Error{Code: code, Message: message, RetryAfter: retryAfter}
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Is reports whether target is an *Error with the same Code, so errors.Is(err, ErrNotFound)
// matches whatever the message says.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t != nil && t.Code == e.Code
}

// Sentinels for errors.Is. Adapters may return them as-is (e.g. a repository returns ErrNotFound);
// callers compare with errors.Is, which matches by code. Never modify a sentinel.
var (
	ErrInvalidInput          = NewError(CodeInvalidInput, "invalid input")
	ErrNotFound              = NewError(CodeNotFound, "resource not found")
	ErrVersionConflict       = NewError(CodeVersionConflict, "resource was changed by another request")
	ErrPortfolioNameConflict = NewError(CodePortfolioNameConflict, "portfolio name already exists")
	ErrPortfolioNotEmpty     = NewError(CodePortfolioNotEmpty, "portfolio still has transactions")
	ErrIdentityConflict      = NewError(CodeIdentityConflict, "identity is already linked")
	ErrIdempotencyConflict   = NewError(CodeIdempotencyConflict, "idempotency key was used with a different request")
	ErrInternal              = NewError(CodeInternal, "internal error")

	// Auth outcomes. Their messages are shown to clients, so they never say which check failed.
	ErrUnauthenticated     = NewError(CodeUnauthenticated, "authentication required")
	ErrInvalidCredentials  = NewError(CodeInvalidCredentials, "email or password is incorrect")
	ErrInvalidVerification = NewError(CodeInvalidVerification, "verification code is invalid or has expired")
	ErrEmailNotVerified    = NewError(CodeEmailNotVerified, "email address is not verified; ask for a new verification code")
	ErrCSRFFailed          = NewError(CodeCSRFFailed, "CSRF token is missing, invalid or expired")

	// Google sign-in. ErrIdentityRejected means the provider or the token check refused the
	// sign-in; the callback turns it into an audited "failed" outcome, never a client message.
	ErrFeatureDisabled  = NewError(CodeFeatureDisabled, "this feature is not configured on this server")
	ErrIdentityRejected = NewError(CodeIdentityRejected, "the identity provider did not confirm the sign-in")
)

// invalidInput reports a rule a client-supplied value broke. field is the API field name.
func invalidInput(field, rule string) *Error {
	return NewError(CodeInvalidInput, field+" "+rule)
}
