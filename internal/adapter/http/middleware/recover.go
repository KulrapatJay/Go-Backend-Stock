package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/gofiber/fiber/v3"
)

// ErrPanic is returned to the error handler after a panic has been recovered.
var ErrPanic = errors.New("panic recovered")

const maxPanicValueLen = 512

// Recover turns a panic into ErrPanic and logs the panic value and stack server-side only.
func Recover(log *slog.Logger) fiber.Handler {
	return func(c fiber.Ctx) (err error) {
		defer func() {
			if r := recover(); r != nil {
				value := fmt.Sprint(r)
				if len(value) > maxPanicValueLen {
					value = value[:maxPanicValueLen]
				}
				log.Error("panic recovered",
					slog.String("event", "http.panic"),
					slog.String("request_id", GetRequestID(c)),
					slog.String("panic", value),
					slog.String("stack", string(debug.Stack())),
				)
				err = ErrPanic
			}
		}()
		return c.Next()
	}
}
