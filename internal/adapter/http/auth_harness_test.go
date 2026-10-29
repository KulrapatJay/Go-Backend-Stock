package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-backend-stock/internal/adapter/postgres"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
	"go-backend-stock/internal/core/service"
)

// integrationURL returns a database URL from the environment. Integration tests skip without it,
// except under REQUIRE_INTEGRATION=1 (CI and make test-integration), where they fail.
func integrationURL(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		if os.Getenv("REQUIRE_INTEGRATION") == "1" {
			t.Fatalf("%s is required when REQUIRE_INTEGRATION=1", key)
		}
		t.Skipf("%s not set; skipping integration test", key)
	}
	return v
}

func openTestPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fakeMailer records what the use cases ask to send, by recipient.
type fakeMailer struct {
	mu            sync.Mutex
	verifications map[string][]domain.OTPMail
	attempts      map[string]int
	resets        map[string][]domain.OTPMail
	changed       map[string]int
	err           error
}

func newFakeMailer() *fakeMailer {
	return &fakeMailer{
		verifications: map[string][]domain.OTPMail{}, attempts: map[string]int{},
		resets: map[string][]domain.OTPMail{}, changed: map[string]int{},
	}
}

func (m *fakeMailer) SendEmailVerification(_ context.Context, mail domain.OTPMail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verifications[mail.To.String()] = append(m.verifications[mail.To.String()], mail)
	return m.err
}

func (m *fakeMailer) SendRegistrationAttempt(_ context.Context, to domain.Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[to.String()]++
	return m.err
}

func (m *fakeMailer) SendPasswordReset(_ context.Context, mail domain.OTPMail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resets[mail.To.String()] = append(m.resets[mail.To.String()], mail)
	return m.err
}

func (m *fakeMailer) SendPasswordChanged(_ context.Context, to domain.Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.changed[to.String()]++
	return m.err
}

func (m *fakeMailer) lastReset(t *testing.T, email string) domain.OTPMail {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	mails := m.resets[email]
	if len(mails) == 0 {
		t.Fatalf("no password reset email was sent to %s", email)
	}
	return mails[len(mails)-1]
}

func (m *fakeMailer) resetCount(email string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.resets[email])
}

func (m *fakeMailer) changedCount(email string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.changed[email]
}

func (m *fakeMailer) lastVerification(t *testing.T, email string) domain.OTPMail {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	mails := m.verifications[email]
	if len(mails) == 0 {
		t.Fatalf("no verification email was sent to %s", email)
	}
	return mails[len(mails)-1]
}

func (m *fakeMailer) verificationCount(email string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.verifications[email])
}

func (m *fakeMailer) attemptCount(email string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempts[email]
}

// failingAuditUoW runs transactions whose audit repository always fails, to prove that a business
// change never commits without its event.
type failingAuditUoW struct{ port.UnitOfWork }

func (u failingAuditUoW) WithinTransaction(ctx context.Context, fn func(port.TxRepositories) error) error {
	return u.UnitOfWork.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		return fn(failingAuditTx{tx})
	})
}

type failingAuditTx struct{ port.TxRepositories }

func (failingAuditTx) Audit() port.AuditRepository { return failingAudit{} }

type failingAudit struct{}

func (failingAudit) Append(context.Context, domain.AuditEvent) error {
	return errors.New("audit store is down")
}

type harnessConfig struct {
	hasher    service.Argon2Params
	floor     time.Duration
	failAudit bool
	identity  port.IdentityProvider // nil = Google sign-in off
}

// testOIDCSecret keys the PKCE verifiers of every harness, so a flow started on one harness can be
// completed on another that shares the database.
const testOIDCSecret = "iiiiiiiiiiiiiiiiiiiiiiiiiiiiiiii"

func withIdentityProvider(p port.IdentityProvider) harnessOption {
	return func(c *harnessConfig) { c.identity = p }
}

type harnessOption func(*harnessConfig)

func withHasherParams(p service.Argon2Params) harnessOption {
	return func(c *harnessConfig) { c.hasher = p }
}

func withResponseFloor(d time.Duration) harnessOption {
	return func(c *harnessConfig) { c.floor = d }
}

func withFailingAudit() harnessOption {
	return func(c *harnessConfig) { c.failAudit = true }
}

// authHarness is the real server over a real PostgreSQL (runtime role), with a fake mailer and a
// clock the test moves. admin reads tables the runtime role may not (audit_events).
type authHarness struct {
	app    *fiber.App
	pool   *pgxpool.Pool
	admin  *pgxpool.Pool
	store  *postgres.Store
	mailer *fakeMailer
	clock  *testClock
	logs   *syncBuffer
	origin string
}

func newAuthHarness(t *testing.T, opts ...harnessOption) *authHarness {
	t.Helper()
	runtimeURL := integrationURL(t, "TEST_RUNTIME_DATABASE_URL")
	adminURL := integrationURL(t, "TEST_ADMIN_DATABASE_URL")
	cfg := harnessConfig{hasher: service.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}}
	for _, o := range opts {
		o(&cfg)
	}

	pool := openTestPool(t, runtimeURL)
	admin := openTestPool(t, adminURL)
	store := postgres.NewStore(pool)
	clock := newTestClock(time.Now())
	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	hasher, err := service.NewPasswordHasher(service.PasswordHasherConfig{Params: cfg.hasher, Concurrency: 4, Logger: log})
	must(err)
	blocklist, err := service.NewBlocklist(strings.NewReader("p@ssw0rd\n"))
	must(err)
	policy, err := service.NewPasswordPolicy(blocklist)
	must(err)
	otp, err := service.NewOTPCodec([]byte(strings.Repeat("o", 32)))
	must(err)
	csrf, err := service.NewCSRFCodec([]byte(strings.Repeat("c", 32)), clock)
	must(err)
	keys, err := service.NewRateLimitKeys([]byte(strings.Repeat("r", 32)))
	must(err)
	limits, err := service.NewRateLimitPolicy(keys, service.RateLimitConfig{
		OTPResendCooldown: time.Minute, OTPRequestsPerHourEmail: 5, OTPRequestsPerHourIP: 20,
		LoginAttemptsPerEmail: 10, LoginAttemptsPerIP: 30,
	})
	must(err)
	pacer, err := service.NewPacer(clock, cfg.floor, 0)
	must(err)
	mailer := newFakeMailer()
	var uow port.UnitOfWork = store
	if cfg.failAudit {
		uow = failingAuditUoW{store}
	}
	oidc, err := service.NewOIDCCodec([]byte(testOIDCSecret))
	must(err)
	auth, err := service.NewAuth(service.AuthDeps{
		Store: store, UnitOfWork: uow, Clock: clock, Hasher: hasher, OTP: otp, Limits: limits,
		Pacer: pacer, Mailer: mailer, Logger: log, SessionTTL: 7 * 24 * time.Hour, OTPMaxAttempts: 5,
		Identity: cfg.identity, OIDC: oidc,
	})
	must(err)

	app := NewServer(Options{Logger: log, Readiness: pool, Auth: AuthOptions{
		Service: auth, Verification: auth, PasswordReset: auth.PasswordReset(), Passwords: policy, CSRF: csrf,
		ClientIP: NewClientIPResolver(testTrustedProxies), PublicOrigin: testOrigin,
	}})
	return &authHarness{app: app, pool: pool, admin: admin, store: store, mailer: mailer, clock: clock, logs: logs, origin: testOrigin}
}

// browser plays one browser: it keeps cookies like a cookie jar, sends Origin and the CSRF token on
// unsafe requests, and comes from its own random IP so tests never share a rate-limit key.
type browser struct {
	h    *authHarness
	t    *testing.T
	ip   string
	jar  map[string]string
	csrf string
}

func (h *authHarness) newBrowser(t *testing.T) *browser {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	raw[0], raw[1], raw[2], raw[3] = 0x20, 0x01, 0x0d, 0xb8 // 2001:db8::/32, the documentation range
	return &browser{h: h, t: t, ip: netip.AddrFrom16(raw).String(), jar: map[string]string{}}
}

// tryDo sends one request without failing the test, so it may run on any goroutine. A string body
// is sent as is; anything else is marshalled to JSON. extra headers override the defaults ("" deletes).
func (b *browser) tryDo(method, path string, body any, extra map[string]string) (response, []byte, error) {
	var reader io.Reader
	switch v := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return response{}, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Forwarded-For", b.ip)
	req.Header.Set("User-Agent", "integration-test/1.0")
	if method != nethttp.MethodGet && method != nethttp.MethodHead {
		req.Header.Set("Origin", b.h.origin)
		if b.csrf != "" {
			req.Header.Set("X-CSRF-Token", b.csrf)
		}
	}
	for name, value := range b.jar {
		req.AddCookie(&nethttp.Cookie{Name: name, Value: value})
	}
	for k, v := range extra {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := b.h.app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second})
	if err != nil {
		return response{}, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, nil, err
	}
	for _, c := range resp.Cookies() {
		if c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now())) {
			delete(b.jar, c.Name)
		} else {
			b.jar[c.Name] = c.Value
		}
	}
	return response{StatusCode: resp.StatusCode, Header: resp.Header}, data, nil
}

func (b *browser) do(method, path string, body any, extra map[string]string) (response, []byte) {
	b.t.Helper()
	resp, data, err := b.tryDo(method, path, body, extra)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp, data
}

// fetchCSRF asks for a token the way a page would before every POST.
func (b *browser) fetchCSRF() {
	b.t.Helper()
	resp, body := b.do(nethttp.MethodGet, "/api/v1/auth/csrf", nil, nil)
	if resp.StatusCode != nethttp.StatusOK {
		b.t.Fatalf("GET /api/v1/auth/csrf = %d %s", resp.StatusCode, body)
	}
	var out CSRFResponse
	if err := json.Unmarshal(body, &out); err != nil {
		b.t.Fatal(err)
	}
	b.csrf = out.CSRFToken
}

func (b *browser) register(email, password string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/register", map[string]string{"email": email, "password": password}, nil)
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return "user-" + mustTestID(t).String() + "@example.com"
}

// stripJSONField re-encodes body without field, so two responses that differ only in a random value
// can be compared byte for byte.
func stripJSONField(t *testing.T, body []byte, path ...string) string {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	m := v
	for _, key := range path[:len(path)-1] {
		inner, ok := m[key].(map[string]any)
		if !ok {
			t.Fatalf("body %s has no object %q", body, key)
		}
		m = inner
	}
	delete(m, path[len(path)-1])
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

type userRow struct {
	ID           string
	PasswordHash string
	Verified     bool
}

func (h *authHarness) user(t *testing.T, email string) (userRow, bool) {
	t.Helper()
	var u userRow
	err := h.admin.QueryRow(context.Background(),
		`SELECT id::text, COALESCE(password_hash, ''), email_verified_at IS NOT NULL FROM app.users WHERE email = $1`, email,
	).Scan(&u.ID, &u.PasswordHash, &u.Verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return userRow{}, false
	}
	if err != nil {
		t.Fatalf("read user: %v", err)
	}
	return u, true
}

type challengeRow struct {
	ID             string
	Purpose        string
	FailedAttempts int
	Open           bool
}

func (h *authHarness) challenges(t *testing.T, userID string) []challengeRow {
	t.Helper()
	rows, err := h.admin.Query(context.Background(),
		`SELECT id::text, purpose, failed_attempts, consumed_at IS NULL AND invalidated_at IS NULL
		 FROM app.otp_challenges WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		t.Fatalf("read challenges: %v", err)
	}
	defer rows.Close()
	var out []challengeRow
	for rows.Next() {
		var c challengeRow
		if err := rows.Scan(&c.ID, &c.Purpose, &c.FailedAttempts, &c.Open); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type auditRow struct {
	EventType string
	Actor     string // "" when NULL
	SessionID string // "" when NULL
	TargetID  string
	Outcome   string
	Reason    string
	Metadata  map[string]any
}

func (h *authHarness) queryAudit(t *testing.T, where string, arg string) []auditRow {
	t.Helper()
	rows, err := h.admin.Query(context.Background(),
		`SELECT event_type, COALESCE(actor_user_id::text, ''), COALESCE(session_id::text, ''),
		        COALESCE(target_id, ''), outcome, COALESCE(reason_code, ''), metadata
		 FROM app.audit_events WHERE `+where+` ORDER BY occurred_at, id`, arg)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		var metadata []byte
		if err := rows.Scan(&r.EventType, &r.Actor, &r.SessionID, &r.TargetID, &r.Outcome, &r.Reason, &metadata); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(metadata, &r.Metadata); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// auditFor returns the events of one request, by the X-Request-ID the response carried.
func (h *authHarness) auditFor(t *testing.T, requestID string) []auditRow {
	t.Helper()
	return h.queryAudit(t, "request_id = $1::uuid", requestID)
}

// auditByTarget returns every event about one target (a user, challenge or session ID).
func (h *authHarness) auditByTarget(t *testing.T, targetID string) []auditRow {
	t.Helper()
	return h.queryAudit(t, "target_id = $1", targetID)
}

func eventTypes(rows []auditRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.EventType
	}
	return out
}

func (b *browser) requestCode(email string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/email-verification/request", map[string]string{"email": email}, nil)
}

func (b *browser) requestReset(email string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/password-reset/request", map[string]string{"email": email}, nil)
}

func (b *browser) confirm(challengeID, otp string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/email-verification/confirm", map[string]string{"challenge_id": challengeID, "otp": otp}, nil)
}

// signUpAndVerify registers email and confirms it with the code the fake mailer received (it
// records the normalized, lower-case address).
func (b *browser) signUpAndVerify(email, password string) {
	b.t.Helper()
	if resp, body := b.register(email, password); resp.StatusCode != nethttp.StatusAccepted {
		b.t.Fatalf("register = %d %s", resp.StatusCode, body)
	}
	mail := b.h.mailer.lastVerification(b.t, strings.ToLower(email))
	if resp, body := b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal()); resp.StatusCode != nethttp.StatusNoContent {
		b.t.Fatalf("confirm = %d %s", resp.StatusCode, body)
	}
}

// wrongCode returns a 6-digit code that differs from code.
func wrongCode(code string) string {
	first := code[0] - '0'
	return string(rune('0'+(first+1)%10)) + code[1:]
}

func (b *browser) login(email, password string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password}, nil)
}

func (b *browser) me() (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, "/api/v1/auth/me", nil, nil)
}

// logout asks for a token first, as a page would: the binding changed at login.
func (b *browser) logout() (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/logout", nil, nil)
}

func (h *authHarness) sessionsOf(t *testing.T, userID string) int {
	t.Helper()
	var n int
	if err := h.admin.QueryRow(context.Background(), `SELECT count(*) FROM app.sessions WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}
