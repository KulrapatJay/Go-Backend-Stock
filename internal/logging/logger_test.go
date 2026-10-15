package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewWritesJSONWithUTCTimeAndService(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo, "stock-api")

	log.Info("server started", slog.String("request_id", "req-1"))

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	ts, _ := entry["time"].(string)
	if !strings.HasSuffix(ts, "Z") {
		t.Errorf("time = %q, want UTC with Z suffix", ts)
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("time %q is not RFC3339: %v", ts, err)
	}
	for key, want := range map[string]string{
		"service":    "stock-api",
		"level":      "INFO",
		"msg":        "server started",
		"request_id": "req-1",
	} {
		if got, _ := entry[key].(string); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo, "stock-api")

	log.Debug("hidden")

	if buf.Len() != 0 {
		t.Fatalf("debug record written at info level: %q", buf.String())
	}
}
