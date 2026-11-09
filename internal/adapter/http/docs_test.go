package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newDocsServer(t *testing.T, docs DocsOptions) *fiber.App {
	t.Helper()
	log, _ := newTestLogger()
	return NewServer(Options{Logger: log, Readiness: fakeReadiness{}, Docs: docs})
}

func enabledDocs(tryItOut bool) DocsOptions {
	return DocsOptions{
		Enabled:         true,
		TryItOutEnabled: tryItOut,
		SpecJSON:        []byte(`{"swagger":"2.0"}`),
		SpecYAML:        []byte("swagger: \"2.0\"\n"),
	}
}

func TestDocsDisabledReturns404(t *testing.T) {
	app := newDocsServer(t, DocsOptions{Enabled: false})
	for _, path := range []string{"/swagger.json", "/swagger.yaml", "/docs", "/docs/"} {
		resp, _ := doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, path, nil))
		if resp.StatusCode != nethttp.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 when docs are disabled", path, resp.StatusCode)
		}
	}
}

func TestDocsServesSpecAndUI(t *testing.T) {
	app := newDocsServer(t, enabledDocs(false))

	resp, body := doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/swagger.json", nil))
	if resp.StatusCode != nethttp.StatusOK || string(body) != `{"swagger":"2.0"}` ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("swagger.json: status=%d type=%q body=%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	resp, body = doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/swagger.yaml", nil))
	if resp.StatusCode != nethttp.StatusOK || !strings.Contains(string(body), "swagger:") ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "application/yaml") {
		t.Errorf("swagger.yaml: status=%d type=%q body=%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	// Both forms serve the UI directly (no redirect): a proxy that strips or adds the trailing
	// slash (Next.js trailingSlash) must not be able to bounce the browser between them.
	// Asset URLs stay relative so a reverse-proxy path prefix keeps working.
	for _, tt := range []struct{ path, assetPrefix string }{
		{"/docs/", ""},
		{"/docs", "docs/"},
		{"/docs/?google=success", ""},
	} {
		resp, body = doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, tt.path, nil))
		if resp.StatusCode != nethttp.StatusOK || resp.Header.Get("Location") != "" {
			t.Errorf("%s: status=%d location=%q, want 200 without redirect", tt.path, resp.StatusCode, resp.Header.Get("Location"))
			continue
		}
		for _, asset := range []string{
			`href="` + tt.assetPrefix + `swagger-ui.css"`,
			`src="` + tt.assetPrefix + `swagger-ui-bundle.js"`,
			`src="` + tt.assetPrefix + `swagger-initializer.js"`,
		} {
			if !strings.Contains(string(body), asset) {
				t.Errorf("%s: body missing %s:\n%s", tt.path, asset, body)
			}
		}
		csp := resp.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "http") {
			t.Errorf("%s: CSP = %q, want self-only scripts without remote origins", tt.path, csp)
		}
	}

	resp, _ = doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/docs/swagger-ui-bundle.js", nil))
	if resp.StatusCode != nethttp.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Errorf("bundle: status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	for _, path := range []string{"/docs/unknown.js", "/docs/LICENSE", "/docs/index.html"} {
		resp, _ = doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, path, nil))
		if resp.StatusCode != nethttp.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 for non-allowlisted file", path, resp.StatusCode)
		}
	}
}

func TestDocsInitializerRespectsTryItOut(t *testing.T) {
	tests := []struct {
		tryItOut bool
		want     string
	}{
		{false, "supportedSubmitMethods: []"},
		{true, `supportedSubmitMethods: ["get", "post", "put", "patch", "delete"]`},
	}
	for _, tt := range tests {
		app := newDocsServer(t, enabledDocs(tt.tryItOut))
		resp, body := doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/docs/swagger-initializer.js", nil))
		if resp.StatusCode != nethttp.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		for _, want := range []string{tt.want, "validatorUrl: null", `new URL("../swagger.json", document.currentScript.src)`} {
			if !strings.Contains(string(body), want) {
				t.Errorf("tryItOut=%v initializer missing %q:\n%s", tt.tryItOut, want, body)
			}
		}
	}
}
