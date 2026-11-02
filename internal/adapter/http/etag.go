package http

import (
	"strconv"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

// setETag sets the strong ETag of a portfolio or transaction: its version in quotes. A client sends
// it back in If-Match to say which version it last read.
func setETag(c fiber.Ctx, v domain.Version) {
	c.Set(fiber.HeaderETag, `"`+strconv.FormatInt(int64(v.Int32()), 10)+`"`)
}
