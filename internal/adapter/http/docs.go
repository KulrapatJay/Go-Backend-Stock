package http

import (
	"io/fs"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/assets"
)

// DocsOptions controls the Swagger UI and spec endpoints. Disabled docs register no routes.
type DocsOptions struct {
	Enabled         bool
	TryItOutEnabled bool
	SpecJSON        []byte
	SpecYAML        []byte
}

const docsCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; " +
	"base-uri 'none'; form-action 'self'"

// docsAssets is the allowlist of files served under /docs/; anything else is 404.
var docsAssets = map[string]string{
	"swagger-ui.css":                  "text/css; charset=utf-8",
	"swagger-ui-bundle.js":            "text/javascript; charset=utf-8",
	"swagger-ui-standalone-preset.js": "text/javascript; charset=utf-8",
	"favicon-32x32.png":               "image/png",
}

func registerDocs(app *fiber.App, opts DocsOptions) {
	if !opts.Enabled {
		return
	}
	getAndHead(app, "/swagger.json", sendBytes("application/json", opts.SpecJSON))
	getAndHead(app, "/swagger.yaml", sendBytes("application/yaml", opts.SpecYAML))
	getAndHead(app, "/docs", func(c fiber.Ctx) error {
		// Relative so the redirect also works behind a reverse proxy path prefix.
		c.Set(fiber.HeaderLocation, "docs/")
		return c.SendStatus(fiber.StatusMovedPermanently)
	})
	getAndHead(app, "/docs/", func(c fiber.Ctx) error {
		return sendAsset(c, "index.html", "text/html; charset=utf-8")
	})
	getAndHead(app, "/docs/swagger-initializer.js", sendDocsBytes("text/javascript; charset=utf-8", swaggerInitializer(opts.TryItOutEnabled)))
	getAndHead(app, "/docs/:file", func(c fiber.Ctx) error {
		name := c.Params("file")
		contentType, ok := docsAssets[name]
		if !ok {
			return fiber.ErrNotFound
		}
		return sendAsset(c, name, contentType)
	})
}

func sendBytes(contentType string, body []byte) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, contentType)
		return c.Send(body)
	}
}

func sendDocsBytes(contentType string, body []byte) fiber.Handler {
	return func(c fiber.Ctx) error {
		setDocsHeaders(c)
		c.Set(fiber.HeaderContentType, contentType)
		return c.Send(body)
	}
}

func sendAsset(c fiber.Ctx, name, contentType string) error {
	body, err := fs.ReadFile(assets.SwaggerUI, "swagger-ui/"+name)
	if err != nil {
		return fiber.ErrNotFound
	}
	setDocsHeaders(c)
	c.Set(fiber.HeaderContentType, contentType)
	return c.Send(body)
}

func setDocsHeaders(c fiber.Ctx) {
	c.Set("Content-Security-Policy", docsCSP)
	c.Set("Cache-Control", "no-cache")
}

func swaggerInitializer(tryItOut bool) []byte {
	methods := "[]"
	if tryItOut {
		methods = `["get", "post", "put", "patch", "delete"]`
	}
	return []byte(`window.addEventListener("load", function () {
  window.ui = SwaggerUIBundle({
    url: "../swagger.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    validatorUrl: null,
    supportedSubmitMethods: ` + methods + `,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    layout: "StandaloneLayout"
  });
});
`)
}
