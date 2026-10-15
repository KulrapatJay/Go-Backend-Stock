package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
)

func TestHealthz(t *testing.T) {
	app, _ := newTestServer(t, fakeReadiness{})

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/healthz", nil))

	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got HealthResponse
	if err := json.Unmarshal(body, &got); err != nil || got.Status != "ok" {
		t.Fatalf("body = %s, want {\"status\":\"ok\"}", body)
	}
	if resp.Header.Get(middleware.HeaderRequestID) == "" {
		t.Error("missing X-Request-ID")
	}
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing security headers: %v", resp.Header)
	}
}

func TestReadyzReady(t *testing.T) {
	app, _ := newTestServer(t, fakeReadiness{})

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/readyz", nil))

	var got ReadyResponse
	if resp.StatusCode != nethttp.StatusOK || json.Unmarshal(body, &got) != nil || got.Status != "ready" {
		t.Fatalf("status = %d body = %s, want 200 ready", resp.StatusCode, body)
	}
}

func TestReadyzNotReadyHidesCause(t *testing.T) {
	app, logs := newTestServer(t, fakeReadiness{err: errors.New("dial tcp 10.0.0.5:5432: connection refused")})

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/readyz", nil))

	if resp.StatusCode != nethttp.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	got := decodeError(t, body)
	if got.Code != "not_ready" || got.RequestID != resp.Header.Get(middleware.HeaderRequestID) {
		t.Errorf("error = %+v", got)
	}
	if strings.Contains(string(body), "10.0.0.5") {
		t.Errorf("response leaked connection details: %s", body)
	}
	if !strings.Contains(logs.String(), "connection refused") {
		t.Error("readiness failure cause was not logged server-side")
	}
}

func TestUnknownRouteReturnsEnvelope404(t *testing.T) {
	app, _ := newTestServer(t, fakeReadiness{})

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/nope", nil))

	if resp.StatusCode != nethttp.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	got := decodeError(t, body)
	if got.Code != "not_found" || got.RequestID == "" || got.RequestID != resp.Header.Get(middleware.HeaderRequestID) {
		t.Errorf("error = %+v", got)
	}
}

func TestAPIErrorIsRenderedAndLoggedWithItsStatus(t *testing.T) {
	log, logs := newTestLogger()
	app := newApp(log)
	app.Get("/conflict", func(c fiber.Ctx) error {
		return &APIError{Status: fiber.StatusConflict, Code: "portfolio_name_conflict", Message: "Portfolio name already exists"}
	})
	app.Use(middleware.NotFound())

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/conflict", nil))

	if resp.StatusCode != nethttp.StatusConflict || decodeError(t, body).Code != "portfolio_name_conflict" {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	entries := accessEntries(t, logs)
	if len(entries) != 1 || int(entries[0]["status"].(float64)) != nethttp.StatusConflict {
		t.Errorf("access log = %v, want one entry with status 409", entries)
	}
}

func TestPanicReturnsGeneric500AndLogsFinalStatus(t *testing.T) {
	log, logs := newTestLogger()
	app := newApp(log)
	app.Get("/boom", func(c fiber.Ctx) error { panic("db password=hunter2 leaked in panic") })
	app.Use(middleware.NotFound())

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/boom", nil))

	if resp.StatusCode != nethttp.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if got := decodeError(t, body); got.Code != "internal" {
		t.Errorf("code = %q, want internal", got.Code)
	}
	for _, leak := range []string{"hunter2", "goroutine", "panic"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("response contains %q: %s", leak, body)
		}
	}
	entries := accessEntries(t, logs)
	if len(entries) != 1 || int(entries[0]["status"].(float64)) != nethttp.StatusInternalServerError {
		t.Errorf("access log = %v, want one entry with status 500", entries)
	}
}

// TestBodyLimitReturns413Envelope uses a real listener: app.Test() hands the fasthttp
// body-limit error straight back to the caller and never reaches the server error handler.
func TestBodyLimitReturns413Envelope(t *testing.T) {
	log, logs := newTestLogger()
	app := NewServer(Options{Logger: log, Readiness: fakeReadiness{}})
	baseURL, stop := listen(t, app)

	resp, err := nethttp.Post(baseURL+"/healthz", "application/json", bytes.NewReader(make([]byte, 64*1024+1)))
	if err != nil {
		t.Fatalf("POST error = %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	stop() // waits for in-flight handlers, so reading the log buffer below is race-free

	if resp.StatusCode != nethttp.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	got := decodeError(t, body)
	if got.Code != "payload_too_large" || got.RequestID == "" || got.RequestID != resp.Header.Get(middleware.HeaderRequestID) {
		t.Errorf("error = %+v, want payload_too_large with request_id matching X-Request-ID", got)
	}
	entries := accessEntries(t, logs)
	if len(entries) != 1 {
		t.Fatalf("access log = %v, want exactly one entry", entries)
	}
	if status := int(entries[0]["status"].(float64)); status != nethttp.StatusRequestEntityTooLarge {
		t.Errorf("logged status = %d, want 413 (the final status)", status)
	}
	if entries[0]["request_id"] != got.RequestID {
		t.Errorf("logged request_id = %v, want %s", entries[0]["request_id"], got.RequestID)
	}
}

// HEAD must mirror GET for every read route: probes and proxies use it. Fiber registers its
// automatic HEAD routes at startup, after the catch-all NotFound middleware, so they never match.
func TestHeadMirrorsGetForReadRoutes(t *testing.T) {
	app := newDocsServer(t, enabledDocs(false))
	for _, path := range []string{"/healthz", "/readyz", "/swagger.json", "/swagger.yaml", "/docs/", "/docs/swagger-ui.css"} {
		resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodHead, path, nil))
		if resp.StatusCode != nethttp.StatusOK {
			t.Errorf("HEAD %s status = %d, want 200", path, resp.StatusCode)
		}
		if len(body) != 0 {
			t.Errorf("HEAD %s returned a %d byte body, want none", path, len(body))
		}
	}
}
