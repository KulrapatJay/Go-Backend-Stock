// Package domain holds the core's entities, value types and errors. It depends only on the
// standard library, github.com/shopspring/decimal and github.com/google/uuid.
//
// Values that come from outside the core are converted with Parse* functions, which return
// invalid_input errors. New* constructors check values the server builds itself and return
// internal errors, because a violation there is a bug rather than bad user input.
package domain

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
)

// Error is the single domain error type. Message is safe to return to clients: it names the
// field and the rule, and never contains the value the client sent.
type Error struct {
	Code    ErrorCode
	Message string
}

// NewError returns an *Error with the given code and client-safe message.
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
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
)

// invalidInput reports a rule a client-supplied value broke. field is the API field name.
func invalidInput(field, rule string) *Error {
	return NewError(CodeInvalidInput, field+" "+rule)
}
