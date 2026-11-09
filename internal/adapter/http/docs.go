package http

import (
	"io/fs"
	"strings"

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
	// /docs and /docs/ both serve the UI instead of redirecting one to the other: a proxy that
	// normalizes the trailing slash (e.g. Next.js strips it) would otherwise loop forever.
	// Asset URLs stay relative to keep working behind a reverse-proxy path prefix, so the
	// slashless page resolves them through the "docs/" segment.
	index, err := fs.ReadFile(assets.SwaggerUI, "swagger-ui/index.html")
	if err != nil {
		panic("swagger-ui/index.html missing from embedded assets: " + err.Error())
	}
	const htmlType = "text/html; charset=utf-8"
	getAndHead(app, "/docs", sendDocsBytes(htmlType, prefixAssetURLs(index, "docs/")))
	getAndHead(app, "/docs/", sendDocsBytes(htmlType, index))
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

// prefixAssetURLs rewrites the relative href/src attributes of the embedded index.html.
func prefixAssetURLs(index []byte, prefix string) []byte {
	out := strings.ReplaceAll(string(index), `href="`, `href="`+prefix)
	return []byte(strings.ReplaceAll(out, `src="`, `src="`+prefix))
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
	// The spec URL is resolved from this script's own URL (always .../docs/swagger-initializer.js),
	// so it is correct whether the page was opened as /docs or /docs/, with or without a path prefix.
	return []byte(`var specUrl = new URL("../swagger.json", document.currentScript.src).href;
window.addEventListener("load", function () {
  window.ui = SwaggerUIBundle({
    url: specUrl,
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
