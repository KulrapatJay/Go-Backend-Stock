package http

import "time"

// CSRFResponse is returned by GET /api/v1/auth/csrf.
type CSRFResponse struct {
	CSRFToken string    `json:"csrf_token" example:"AQAAAABnHk3QxJ9b3m0z0bH2l9V1cW5qQk1xU2ZtV0RkY0p0dVhQb0xMZ3hQYnpqUkw"`
	ExpiresAt time.Time `json:"expires_at" example:"2026-10-24T10:00:00Z"`
}
