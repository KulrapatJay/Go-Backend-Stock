package middleware

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func logEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("log line is not JSON: %v (%q)", err, scanner.Text())
		}
		entries = append(entries, entry)
	}
	return entries
}

func findEntry(t *testing.T, entries []map[string]any, msg string) map[string]any {
	t.Helper()
	for _, e := range entries {
		if e["msg"] == msg {
			return e
		}
	}
	t.Fatalf("no log entry with msg %q in %v", msg, entries)
	return nil
}

// doRequest runs the request through the app and always closes the body.
func doRequest(t *testing.T, app *fiber.App, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

// testErrorHandler stands in for the production envelope handler, which lives in a parent package.
func testErrorHandler(c fiber.Ctx, err error) error {
	status := fiber.StatusInternalServerError
	var fe *fiber.Error
	if errors.As(err, &fe) {
		status = fe.Code
	}
	return c.Status(status).SendString(http.StatusText(status))
}

func newChainApp(log *slog.Logger) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler, StrictRouting: true, CaseSensitive: true})
	app.Use(RequestID())
	app.Use(AccessLog(log, testErrorHandler))
	app.Use(Recover(log))
	return app
}
