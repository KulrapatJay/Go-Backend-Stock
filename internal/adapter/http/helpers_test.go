package http

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

type fakeReadiness struct{ err error }

func (f fakeReadiness) Ping(context.Context) error { return f.err }

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func newTestServer(t *testing.T, readiness ReadinessChecker) (*fiber.App, *bytes.Buffer) {
	t.Helper()
	log, buf := newTestLogger()
	return NewServer(Options{Logger: log, Readiness: readiness}), buf
}

// response is the part of an HTTP response the tests inspect.
type response struct {
	StatusCode int
	Header     nethttp.Header
}

// doRequest runs the request through the app and always closes the body.
func doRequest(t *testing.T, app *fiber.App, req *nethttp.Request) (response, []byte) {
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
	return response{StatusCode: resp.StatusCode, Header: resp.Header}, body
}

func decodeError(t *testing.T, body []byte) ErrorBody {
	t.Helper()
	var envelope ErrorResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("body is not an error envelope: %v (%s)", err, body)
	}
	return envelope.Error
}

func accessEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		if entry["msg"] == "http request" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// listen serves app on a loopback port and returns its base URL and an idempotent stop func.
func listen(t *testing.T, app *fiber.App) (baseURL string, stop func()) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	stop = func() {
		_ = app.ShutdownWithTimeout(5 * time.Second)
		<-done
	}
	t.Cleanup(stop)
	return "http://" + ln.Addr().String(), stop
}
