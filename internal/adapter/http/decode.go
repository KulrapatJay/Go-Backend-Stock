package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

var errMalformedJSON = domain.NewError(domain.CodeMalformedJSON, "request body must be one JSON object with only the documented fields")

// decodeJSON reads the body into dst: exactly one JSON value, no unknown fields, nothing after it
// (phase 1 spec section 11). A missing value is left empty for the Parse* functions to reject.
func decodeJSON(c fiber.Ctx, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(c.Body()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errMalformedJSON
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errMalformedJSON
	}
	return nil
}
