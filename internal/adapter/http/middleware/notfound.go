package middleware

import (
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// NotFound must be registered last. It marks the route as "unmatched" so the access log never
// records the raw path of an unknown URL. When the path exists with other methods (allowed returns
// them) it answers 405 with an Allow header instead of 404.
func NotFound(allowed func(path string) []string) fiber.Handler {
	return func(c fiber.Ctx) error {
		MarkRoute(c, "unmatched")
		if allowed != nil {
			methods := allowed(c.Path())
			// A request whose method is listed is a route Fiber did not match (for example its
			// automatic HEAD route, added after this catch-all): that is a 404, not a 405.
			if len(methods) > 0 && !slices.Contains(methods, c.Method()) {
				c.Set(fiber.HeaderAllow, strings.Join(methods, ", "))
				return fiber.ErrMethodNotAllowed
			}
		}
		return fiber.ErrNotFound
	}
}
