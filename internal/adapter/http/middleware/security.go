package middleware

import "github.com/gofiber/fiber/v3"

// SecurityHeaders sets defaults for API responses. Docs handlers override Cache-Control and add a CSP.
func SecurityHeaders() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Cache-Control", "no-store")
		c.Set("Referrer-Policy", "no-referrer")
		c.Set("X-Frame-Options", "DENY")
		return c.Next()
	}
}
