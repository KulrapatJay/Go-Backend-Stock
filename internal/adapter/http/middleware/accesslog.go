package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"
)

type routeKey struct{}

// MarkRoute overrides the route template recorded by AccessLog.
func MarkRoute(c fiber.Ctx, route string) {
	c.Locals(routeKey{}, route)
}

func routeTemplate(c fiber.Ctx) string {
	if route, ok := c.Locals(routeKey{}).(string); ok && route != "" {
		return route
	}
	return c.Route().Path
}

// AccessLog records one entry per request after the error handler has produced the
// final status, so failed and panicking requests are never logged as 200.
// It logs the route template only: never the raw path, query string, headers or body.
func AccessLog(log *slog.Logger, handleError fiber.ErrorHandler) fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		if chainErr := c.Next(); chainErr != nil {
			if err := handleError(c, chainErr); err != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}

		LogAccess(log, c, start)
		// The error was already rendered by handleError; returning it would render it twice.
		return nil
	}
}

// LogAccess writes the access entry for c using the response status it currently holds.
// Callers must render any error first so the logged status is the final one.
func LogAccess(log *slog.Logger, c fiber.Ctx, start time.Time) {
	status := c.Response().StatusCode()
	level := slog.LevelInfo
	switch {
	case status >= fiber.StatusInternalServerError:
		level = slog.LevelError
	case status >= fiber.StatusBadRequest:
		level = slog.LevelWarn
	}
	log.LogAttrs(context.Background(), level, "http request",
		slog.String("event", "http.access"),
		slog.String("request_id", GetRequestID(c)),
		slog.String("method", c.Method()),
		slog.String("route", routeTemplate(c)),
		slog.Int("status", status),
		slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
	)
}
