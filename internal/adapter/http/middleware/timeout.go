package middleware

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
)

// RequestTimeout gives every request a deadline on its context. Handlers pass c.Context() to the
// services and repositories, so a stuck database call ends with context.DeadlineExceeded (mapped
// to a retryable 503) instead of holding a pooled connection for as long as the client waits.
func RequestTimeout(d time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), d)
		defer cancel()
		c.SetContext(ctx)
		return c.Next()
	}
}
