package main

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// runHealthcheck lets the distroless image probe itself without curl: `api healthcheck`.
// It only ever talks to loopback; HTTP_ADDR contributes just the port.
func runHealthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return 1
	}
	target := url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", port), Path: "/healthz"}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
