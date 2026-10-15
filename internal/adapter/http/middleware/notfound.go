package middleware

import "github.com/gofiber/fiber/v3"

// NotFound must be registered last. It marks the route as "unmatched" so the access log
// never records the raw path of an unknown URL.
func NotFound() fiber.Handler {
	return func(c fiber.Ctx) error {
		MarkRoute(c, "unmatched")
		return fiber.ErrNotFound
	}
}
