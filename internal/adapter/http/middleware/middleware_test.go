package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func TestRequestIDIgnoresClientValue(t *testing.T) {
	log, _ := newTestLogger()
	app := newChainApp(log)
	app.Get("/ok", func(c fiber.Ctx) error { return c.SendString(GetRequestID(c)) })

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set(HeaderRequestID, "client-supplied")
	resp, body := doRequest(t, app, req)

	got := resp.Header.Get(HeaderRequestID)
	if got == "client-supplied" {
		t.Fatal("server reused the client request ID")
	}
	parsed, err := uuid.Parse(got)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("X-Request-ID = %q, want UUIDv7 (err %v)", got, err)
	}
	if string(body) != got {
		t.Errorf("GetRequestID() = %q, header = %q", body, got)
	}
}

func TestRequestIDIsUniquePerRequest(t *testing.T) {
	log, _ := newTestLogger()
	app := newChainApp(log)
	app.Get("/ok", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	first, _ := doRequest(t, app, httptest.NewRequest(http.MethodGet, "/ok", nil))
	second, _ := doRequest(t, app, httptest.NewRequest(http.MethodGet, "/ok", nil))

	if first.Header.Get(HeaderRequestID) == second.Header.Get(HeaderRequestID) {
		t.Fatal("two requests received the same request ID")
	}
}

func TestAccessLogRecordsFinalStatus(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		handler    fiber.Handler
		wantStatus int
		wantLevel  string
	}{
		{"success", "/ok", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) }, 200, "INFO"},
		{"client error", "/bad", func(c fiber.Ctx) error { return fiber.ErrBadRequest }, 400, "WARN"},
		{"conflict", "/conflict", func(c fiber.Ctx) error { return fiber.NewError(fiber.StatusConflict, "conflict") }, 409, "WARN"},
		{"panic", "/panic", func(c fiber.Ctx) error { panic("boom") }, 500, "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, buf := newTestLogger()
			app := newChainApp(log)
			app.Get(tt.path, tt.handler)

			resp, _ := doRequest(t, app, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			entry := findEntry(t, logEntries(t, buf), "http request")
			if got := int(entry["status"].(float64)); got != tt.wantStatus {
				t.Errorf("logged status = %d, want %d", got, tt.wantStatus)
			}
			if entry["level"] != tt.wantLevel {
				t.Errorf("level = %v, want %s", entry["level"], tt.wantLevel)
			}
			if entry["method"] != http.MethodGet || entry["route"] != tt.path || entry["event"] != "http.access" {
				t.Errorf("unexpected access entry: %v", entry)
			}
			if entry["request_id"] != resp.Header.Get(HeaderRequestID) {
				t.Errorf("logged request_id = %v, header = %s", entry["request_id"], resp.Header.Get(HeaderRequestID))
			}
		})
	}
}

func TestRecoverLogsPanicWithRequestID(t *testing.T) {
	log, buf := newTestLogger()
	app := newChainApp(log)
	app.Get("/panic", func(c fiber.Ctx) error { panic("boom") })

	resp, body := doRequest(t, app, httptest.NewRequest(http.MethodGet, "/panic", nil))

	entry := findEntry(t, logEntries(t, buf), "panic recovered")
	if entry["level"] != "ERROR" || entry["panic"] != "boom" {
		t.Errorf("unexpected panic entry: %v", entry)
	}
	if stack, _ := entry["stack"].(string); !strings.Contains(stack, "goroutine") {
		t.Errorf("panic entry has no stack trace: %v", entry)
	}
	if entry["request_id"] != resp.Header.Get(HeaderRequestID) {
		t.Errorf("panic request_id = %v, header = %s", entry["request_id"], resp.Header.Get(HeaderRequestID))
	}
	if strings.Contains(string(body), "goroutine") {
		t.Errorf("response exposed a stack trace: %s", body)
	}
}

func TestAccessLogUsesRouteTemplateAndOmitsSecrets(t *testing.T) {
	log, buf := newTestLogger()
	app := newChainApp(log)
	app.Get("/items/:id", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/items/98765?token=topsecret", nil)
	req.Header.Set("Cookie", "session=cookiesecret")
	req.Header.Set("Authorization", "Bearer authsecret")
	doRequest(t, app, req)

	raw := buf.String()
	for _, secret := range []string{"topsecret", "cookiesecret", "authsecret", "98765"} {
		if strings.Contains(raw, secret) {
			t.Errorf("log contains %q: %s", secret, raw)
		}
	}
	entry := findEntry(t, logEntries(t, buf), "http request")
	if entry["route"] != "/items/:id" {
		t.Errorf("route = %v, want /items/:id", entry["route"])
	}
}

func TestNotFoundMarksUnmatchedRoute(t *testing.T) {
	log, buf := newTestLogger()
	app := newChainApp(log)
	app.Use(NotFound())

	resp, _ := doRequest(t, app, httptest.NewRequest(http.MethodGet, "/nope/secret-path", nil))

	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	entry := findEntry(t, logEntries(t, buf), "http request")
	if entry["route"] != "unmatched" {
		t.Errorf("route = %v, want unmatched", entry["route"])
	}
	if strings.Contains(buf.String(), "secret-path") {
		t.Errorf("raw unmatched path leaked into log: %s", buf.String())
	}
}

func TestSecurityHeaders(t *testing.T) {
	app := fiber.New()
	app.Use(SecurityHeaders())
	app.Get("/ok", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	resp, _ := doRequest(t, app, httptest.NewRequest(http.MethodGet, "/ok", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}
