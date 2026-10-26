package service

import (
	"bytes"
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// The fakes embed the port interface they stand in for: a method a test does not override panics
// when called, which shows at once that a use case touched something unexpected.

type fakeRepositories struct {
	port.Repositories
	sessions *fakeSessions
	limits   *fakeRateLimits
	users    *fakeUsers
}

func (r *fakeRepositories) Sessions() port.SessionRepository     { return r.sessions }
func (r *fakeRepositories) RateLimits() port.RateLimitRepository { return r.limits }
func (r *fakeRepositories) Users() port.UserRepository           { return r.users }

type fakeSessions struct {
	port.SessionRepository
	byHash map[string]domain.Session
	err    error
	calls  int
}

func (f *fakeSessions) ByTokenHash(_ context.Context, hash []byte) (domain.Session, error) {
	f.calls++
	if f.err != nil {
		return domain.Session{}, f.err
	}
	s, ok := f.byHash[string(hash)]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	return s, nil
}

type fakeUsers struct {
	port.UserRepository
	credentials map[string]domain.UserCredentials
}

func (f *fakeUsers) CredentialsByEmail(_ context.Context, email domain.Email) (domain.UserCredentials, error) {
	c, ok := f.credentials[email.String()]
	if !ok {
		return domain.UserCredentials{}, domain.ErrNotFound
	}
	return c, nil
}

type fakeRateLimits struct {
	decision domain.RateLimitDecision
	err      error
	rules    []domain.RateLimitRule
	at       time.Time
}

func (f *fakeRateLimits) Admit(_ context.Context, rules []domain.RateLimitRule, now time.Time) (domain.RateLimitDecision, error) {
	f.rules, f.at = rules, now
	return f.decision, f.err
}

type fakeAudit struct {
	events []domain.AuditEvent
	err    error
}

func (a *fakeAudit) Append(_ context.Context, e domain.AuditEvent) error {
	if a.err != nil {
		return a.err
	}
	a.events = append(a.events, e)
	return nil
}

type fakeTx struct {
	port.TxRepositories
	audit *fakeAudit
}

func (t fakeTx) Audit() port.AuditRepository { return t.audit }

type fakeUoW struct {
	audit *fakeAudit
	runs  int
}

func (u *fakeUoW) WithinTransaction(_ context.Context, fn func(port.TxRepositories) error) error {
	u.runs++
	return fn(fakeTx{audit: u.audit})
}

type fakeMailer struct{ port.Mailer }

type testAuth struct {
	auth  *Auth
	repos *fakeRepositories
	uow   *fakeUoW
	clock *fakeClock
	logs  *bytes.Buffer
}

func newTestAuth(t *testing.T, mutate func(*AuthDeps)) testAuth {
	t.Helper()
	clock := newFakeClock()
	repos := &fakeRepositories{
		sessions: &fakeSessions{byHash: map[string]domain.Session{}},
		limits:   &fakeRateLimits{decision: domain.RateLimitDecision{Allowed: true}},
		users:    &fakeUsers{credentials: map[string]domain.UserCredentials{}},
	}
	uow := &fakeUoW{audit: &fakeAudit{}}
	hasher, _ := newHasher(t, testArgon2Params, 2, 0)
	otp, err := NewOTPCodec(bytes.Repeat([]byte("o"), 32))
	if err != nil {
		t.Fatal(err)
	}
	pacer, err := NewPacer(clock, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	deps := AuthDeps{
		Store: repos, UnitOfWork: uow, Clock: clock, Hasher: hasher, OTP: otp, Limits: newTestPolicy(t),
		Pacer: pacer, Mailer: fakeMailer{}, Logger: slog.New(slog.NewJSONHandler(logs, nil)),
		SessionTTL: 7 * 24 * time.Hour, OTPMaxAttempts: 5,
	}
	if mutate != nil {
		mutate(&deps)
	}
	auth, err := NewAuth(deps)
	if err != nil {
		t.Fatalf("NewAuth() error = %v", err)
	}
	return testAuth{auth: auth, repos: repos, uow: uow, clock: clock, logs: logs}
}

func testMeta(t *testing.T) domain.RequestMeta {
	t.Helper()
	return domain.NewRequestMeta(mustID(t), netip.MustParseAddr("203.0.113.9"), "test-agent/1.0")
}
