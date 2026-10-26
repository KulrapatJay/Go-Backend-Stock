package http

import (
	"bytes"
	"context"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
	"go-backend-stock/internal/core/service"
)

const testOrigin = "http://localhost:8080"

// testTrustedProxies trusts the peer address app.Test uses (0.0.0.0 or loopback), so a test can
// choose its client IP with X-Forwarded-For. If a test sees another peer address, add its range here.
var testTrustedProxies = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
}

// testClock is a port.Clock that only moves when a test says so. Requests may read it from several
// goroutines.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(start time.Time) *testClock {
	return &testClock{now: start.UTC().Truncate(time.Microsecond)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeAuthService embeds the port: a method a test does not set panics when called.
type fakeAuthService struct {
	port.AuthService
	principals map[string]domain.Principal // session token -> caller
	err        error
}

func (f *fakeAuthService) Authenticate(_ context.Context, token string) (domain.Principal, error) {
	if f.err != nil {
		return domain.Principal{}, f.err
	}
	p, ok := f.principals[token]
	if !ok {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	return p, nil
}

func newTestCSRF(t *testing.T, clock port.Clock) *service.CSRFCodec {
	t.Helper()
	codec, err := service.NewCSRFCodec([]byte(strings.Repeat("c", 32)), clock)
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

// newAuthTestServer builds the real server around a fake auth service.
func newAuthTestServer(t *testing.T, auth port.AuthService, secure bool) (*fiber.App, *service.CSRFCodec) {
	t.Helper()
	codec := newTestCSRF(t, newTestClock(time.Now()))
	log, _ := newTestLogger()
	app := NewServer(Options{Logger: log, Readiness: fakeReadiness{}, Auth: AuthOptions{
		Service: auth, CSRF: codec, ClientIP: NewClientIPResolver(testTrustedProxies),
		PublicOrigin: testOrigin, SecureCookies: secure,
	}})
	return app, codec
}

// syncBuffer is a bytes.Buffer that log handlers on several goroutines can share.
type syncBuffer struct { //nolint:unused // the login tests of tasks 9-12 use it.
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) { //nolint:unused // see syncBuffer.
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string { //nolint:unused // see syncBuffer.
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func mustTestID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
