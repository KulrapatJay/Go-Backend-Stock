// Package middleware contains the Fiber middleware chain shared by every route.
package middleware

import (
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// HeaderRequestID carries the server-generated request ID on every response.
const HeaderRequestID = "X-Request-ID"

type requestIDKey struct{}

// RequestID assigns a fresh UUIDv7 to each request. Client-supplied IDs are ignored.
func RequestID() fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := NewRequestID()
		if err != nil {
			return err
		}
		SetRequestID(c, id)
		return c.Next()
	}
}

// NewRequestID returns a new UUIDv7 string.
func NewRequestID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate request id: %w", err)
	}
	return id.String(), nil
}

// GetRequestID returns the request ID stored on c, or "" when none was assigned.
func GetRequestID(c fiber.Ctx) string {
	id, _ := c.Locals(requestIDKey{}).(string)
	return id
}

// SetRequestID stores id on c and echoes it in the response header.
func SetRequestID(c fiber.Ctx, id string) {
	c.Locals(requestIDKey{}, id)
	c.Set(HeaderRequestID, id)
}
