// Package http is the primary adapter: Fiber server, middleware chain, handlers and DTOs.
package http

import (
	"errors"
	"log/slog"
	"math"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

// APIError is an HTTP-level error with a stable machine-readable code.
type APIError struct {
	Status     int
	Code       string
	Message    string
	Details    []string
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// ErrorResponse is the single error envelope used by every endpoint.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries the error code, a human-readable message and the request ID.
type ErrorBody struct {
	Code      string `json:"code" example:"not_found"`
	Message   string `json:"message" example:"Not Found"`
	RequestID string `json:"request_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"`
	// Details lists the rules an input broke, such as password_too_short; only on invalid_input.
	Details []string `json:"details,omitempty" example:"password_too_short,password_missing_special"`
}

// domainStatus maps every domain error code to its HTTP status (phase 1 spec section 11).
var domainStatus = map[domain.ErrorCode]int{
	domain.CodeInvalidInput:          nethttp.StatusBadRequest,
	domain.CodeMalformedJSON:         nethttp.StatusBadRequest,
	domain.CodeUnauthenticated:       nethttp.StatusUnauthorized,
	domain.CodeInvalidCredentials:    nethttp.StatusUnauthorized,
	domain.CodeInvalidReset:          nethttp.StatusBadRequest,
	domain.CodeInvalidVerification:   nethttp.StatusBadRequest,
	domain.CodeCSRFFailed:            nethttp.StatusForbidden,
	domain.CodeAccessDenied:          nethttp.StatusForbidden,
	domain.CodeEmailNotVerified:      nethttp.StatusForbidden,
	domain.CodeVersionConflict:       nethttp.StatusPreconditionFailed,
	domain.CodePreconditionRequired:  nethttp.StatusPreconditionRequired,
	domain.CodeNotFound:              nethttp.StatusNotFound,
	domain.CodeIdentityConflict:      nethttp.StatusConflict,
	domain.CodeIdempotencyConflict:   nethttp.StatusConflict,
	domain.CodePortfolioNotEmpty:     nethttp.StatusConflict,
	domain.CodePortfolioNameConflict: nethttp.StatusConflict,
	domain.CodeInsufficientShares:    nethttp.StatusUnprocessableEntity,
	domain.CodeInvalidHistory:        nethttp.StatusUnprocessableEntity,
	domain.CodeUnknownSymbol:         nethttp.StatusUnprocessableEntity,
	domain.CodeRateLimited:           nethttp.StatusTooManyRequests,
	domain.CodeDependencyUnavailable: nethttp.StatusServiceUnavailable,
	domain.CodeFeatureDisabled:       nethttp.StatusServiceUnavailable,
	domain.CodeServerBusy:            nethttp.StatusServiceUnavailable,
	domain.CodeInternal:              nethttp.StatusInternalServerError,
	domain.CodeAuditWriteFailed:      nethttp.StatusInternalServerError,
}

// ErrorHandler renders every error as ErrorResponse. Server errors are logged with the
// request ID; their details never reach the client.
func ErrorHandler(log *slog.Logger) fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		apiErr := toAPIError(err)

		// Errors raised before the middleware chain (e.g. body too large) have no ID yet.
		requestID := middleware.GetRequestID(c)
		if requestID == "" {
			if id, idErr := middleware.NewRequestID(); idErr == nil {
				middleware.SetRequestID(c, id)
				requestID = id
			}
		}

		if apiErr.Status >= nethttp.StatusInternalServerError {
			log.Error("request failed",
				slog.String("event", "http.error"),
				slog.String("request_id", requestID),
				slog.String("error_code", apiErr.Code),
				slog.String("error", err.Error()),
			)
		}

		c.Set("Cache-Control", "no-store")
		c.Set("X-Content-Type-Options", "nosniff")
		if apiErr.RetryAfter > 0 {
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(retryAfterSeconds(apiErr.RetryAfter)))
		}
		return c.Status(apiErr.Status).JSON(ErrorResponse{Error: ErrorBody{
			Code:      apiErr.Code,
			Message:   apiErr.Message,
			RequestID: requestID,
			Details:   apiErr.Details,
		}})
	}
}

func toAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		return fromDomain(domainErr)
	}
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return &APIError{Status: fiberErr.Code, Code: codeForStatus(fiberErr.Code), Message: nethttp.StatusText(fiberErr.Code)}
	}
	return internalError()
}

// fromDomain maps a domain error. 500s keep their code but never their message, which may name
// stored data; every other message is written for clients (domain.Error contract).
func fromDomain(e *domain.Error) *APIError {
	status, ok := domainStatus[e.Code]
	if !ok {
		return internalError()
	}
	if status == nethttp.StatusInternalServerError {
		return &APIError{Status: status, Code: string(e.Code), Message: "Internal server error"}
	}
	api := &APIError{Status: status, Code: string(e.Code), Message: e.Message, Details: e.Reasons, RetryAfter: e.RetryAfter}
	if e.Code == domain.CodeServerBusy && api.RetryAfter <= 0 {
		api.RetryAfter = time.Second
	}
	return api
}

func internalError() *APIError {
	return &APIError{Status: nethttp.StatusInternalServerError, Code: "internal", Message: "Internal server error"}
}

// retryAfterSeconds rounds up to whole seconds, at least one, so a client never retries too early.
func retryAfterSeconds(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	if s < 1 {
		return 1
	}
	return s
}

func codeForStatus(status int) string {
	switch status {
	case nethttp.StatusNotFound:
		return "not_found"
	case nethttp.StatusMethodNotAllowed:
		return "method_not_allowed"
	case nethttp.StatusRequestTimeout:
		return "request_timeout"
	case nethttp.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case nethttp.StatusTooManyRequests:
		return "rate_limited"
	}
	if status >= nethttp.StatusInternalServerError {
		return "internal"
	}
	return "invalid_input"
}
