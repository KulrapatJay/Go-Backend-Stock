package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serveStatus(t *testing.T, status int) (port string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	return port
}

func TestRunHealthcheck(t *testing.T) {
	tests := []struct {
		name string
		addr func(t *testing.T) string
		want int
	}{
		{"healthy on port only", func(t *testing.T) string { return ":" + serveStatus(t, http.StatusOK) }, 0},
		{"healthy with wildcard host uses loopback", func(t *testing.T) string { return "0.0.0.0:" + serveStatus(t, http.StatusOK) }, 0},
		{"unhealthy status", func(t *testing.T) string { return ":" + serveStatus(t, http.StatusServiceUnavailable) }, 1},
		{"nothing listening", func(t *testing.T) string {
			ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			_, port, _ := net.SplitHostPort(ln.Addr().String())
			ln.Close() // free port: connection refused
			return ":" + port
		}, 1},
		{"address without port", func(*testing.T) string { return "localhost" }, 1},
		{"non numeric port", func(*testing.T) string { return ":http" }, 1},
		{"port out of range", func(*testing.T) string { return ":70000" }, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HTTP_ADDR", tt.addr(t))
			if got := runHealthcheck(); got != tt.want {
				t.Fatalf("runHealthcheck() = %d, want %d", got, tt.want)
			}
		})
	}
}
