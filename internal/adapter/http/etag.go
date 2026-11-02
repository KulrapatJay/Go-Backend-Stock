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

var (
	errIfMatchRequired = domain.NewError(domain.CodePreconditionRequired, "If-Match is required; send the ETag from the last read")
	errIfMatchInvalid  = domain.NewError(domain.CodeInvalidInput, `If-Match must be one ETag in quotes, such as "3"`)
)

// parseIfMatch reads the version a client last saw from If-Match. Only the strong form the API
// issues is accepted: one positive integer in quotes. A weak validator (W/"3"), the wildcard, a
// list or a malformed value is an invalid input, not a missing precondition: the client sent one.
func parseIfMatch(c fiber.Ctx) (domain.Version, error) {
	values := c.Request().Header.PeekAll(fiber.HeaderIfMatch)
	if len(values) == 0 {
		return domain.Version{}, errIfMatchRequired
	}
	if len(values) > 1 {
		return domain.Version{}, errIfMatchInvalid
	}
	raw := string(values[0])
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return domain.Version{}, errIfMatchInvalid
	}
	digits := raw[1 : len(raw)-1]
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return domain.Version{}, errIfMatchInvalid
		}
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return domain.Version{}, errIfMatchInvalid
	}
	v, err := domain.ParseVersion(n)
	if err != nil {
		return domain.Version{}, errIfMatchInvalid
	}
	return v, nil
}
