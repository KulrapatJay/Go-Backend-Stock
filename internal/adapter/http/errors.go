// Package http is the primary adapter: Fiber server, middleware chain, handlers and DTOs.
package http

import (
	"errors"
	"log/slog"
	nethttp "net/http"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
)

// APIError is an HTTP-level error with a stable machine-readable code.
type APIError struct {
	Status  int
	Code    string
	Message string
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
		return c.Status(apiErr.Status).JSON(ErrorResponse{Error: ErrorBody{
			Code:      apiErr.Code,
			Message:   apiErr.Message,
			RequestID: requestID,
		}})
	}
}

func toAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return &APIError{Status: fiberErr.Code, Code: codeForStatus(fiberErr.Code), Message: nethttp.StatusText(fiberErr.Code)}
	}
	return &APIError{Status: nethttp.StatusInternalServerError, Code: "internal", Message: "Internal server error"}
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
