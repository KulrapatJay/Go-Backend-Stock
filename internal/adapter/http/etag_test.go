package http

import (
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

// parseIfMatchHeader runs parseIfMatch on a request carrying the given If-Match header lines.
func parseIfMatchHeader(t *testing.T, lines ...string) (domain.Version, error) {
	t.Helper()
	var (
		version domain.Version
		parsed  error
	)
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		version, parsed = parseIfMatch(c)
		return nil
	})
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/", nil)
	for _, line := range lines {
		req.Header.Add("If-Match", line)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return version, parsed
}

func TestParseIfMatch(t *testing.T) {
	version, err := parseIfMatchHeader(t, `"3"`)
	if err != nil || version.Int32() != 3 {
		t.Errorf(`parseIfMatch("3") = %v, %v; want version 3`, version, err)
	}

	_, err = parseIfMatchHeader(t)
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != domain.CodePreconditionRequired {
		t.Errorf("parseIfMatch(no header) error = %v, want precondition_required", err)
	}

	malformed := map[string][]string{
		`unquoted number`: {`3`},
		`wildcard`:        {`*`},
		`weak validator`:  {`W/"3"`},
		`two values`:      {`"3", "4"`},
		`two headers`:     {`"3"`, `"4"`},
		`zero`:            {`"0"`},
		`leading zero`:    {`"03"`},
		`many zeros`:      {`"0003"`},
		`negative`:        {`"-1"`},
		`explicit plus`:   {`"+3"`},
		`not a number`:    {`"x"`},
		`empty etag`:      {`""`},
		`too large`:       {`"99999999999999999999"`},
		`past int32`:      {`"2147483648"`},
	}
	for name, lines := range malformed {
		t.Run(name, func(t *testing.T) {
			_, err := parseIfMatchHeader(t, lines...)
			if !errors.As(err, &de) || de.Code != domain.CodeInvalidInput || de.Message == "" || de.Message[:8] != "If-Match" {
				t.Errorf("parseIfMatch(%q) error = %v, want invalid_input about If-Match", lines, err)
			}
		})
	}
}
