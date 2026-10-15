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

	resp, _ = doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/docs", nil))
	if resp.StatusCode != nethttp.StatusMovedPermanently || resp.Header.Get("Location") != "docs/" {
		t.Errorf("/docs: status=%d location=%q, want 301 to relative docs/", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, body = doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/docs/", nil))
	if resp.StatusCode != nethttp.StatusOK || !strings.Contains(string(body), "swagger-initializer.js") {
		t.Errorf("/docs/: status=%d body=%s", resp.StatusCode, body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "http") {
		t.Errorf("CSP = %q, want self-only scripts without remote origins", csp)
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
		for _, want := range []string{tt.want, "validatorUrl: null", `url: "../swagger.json"`} {
			if !strings.Contains(string(body), want) {
				t.Errorf("tryItOut=%v initializer missing %q:\n%s", tt.tryItOut, want, body)
			}
		}
	}
}
