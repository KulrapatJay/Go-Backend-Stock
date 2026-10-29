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

// Lock answers with the user a test registered, whatever its email.
func (f *fakeUsers) Lock(_ context.Context, id domain.ID) (domain.User, error) {
	for _, c := range f.credentials {
		if c.User.ID == id {
			return c.User, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
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

// fakeChallenges records what a use case writes to otp_challenges.
type fakeChallenges struct {
	port.OTPChallengeRepository
	invalidated []domain.OTPPurpose
	inserted    []domain.OTPChallenge
}

func (f *fakeChallenges) InvalidateOpen(_ context.Context, _ domain.ID, purpose domain.OTPPurpose, _ time.Time) error {
	f.invalidated = append(f.invalidated, purpose)
	return nil
}

func (f *fakeChallenges) Insert(_ context.Context, c domain.OTPChallenge) error {
	f.inserted = append(f.inserted, c)
	return nil
}

type fakeTx struct {
	port.TxRepositories
	audit      *fakeAudit
	users      *fakeUsers
	challenges *fakeChallenges
}

func (t fakeTx) Audit() port.AuditRepository                { return t.audit }
func (t fakeTx) Users() port.UserRepository                 { return t.users }
func (t fakeTx) OTPChallenges() port.OTPChallengeRepository { return t.challenges }

type fakeUoW struct {
	audit      *fakeAudit
	users      *fakeUsers
	challenges *fakeChallenges
	runs       int
}

func (u *fakeUoW) WithinTransaction(_ context.Context, fn func(port.TxRepositories) error) error {
	u.runs++
	return fn(fakeTx{audit: u.audit, users: u.users, challenges: u.challenges})
}

type fakeMailer struct{ port.Mailer }

// recordingMailer keeps the password reset emails and password-changed notices a use case hands over.
type recordingMailer struct {
	port.Mailer
	resets  []domain.OTPMail
	changed []domain.Email
}

func (m *recordingMailer) SendPasswordChanged(_ context.Context, to domain.Email) error {
	m.changed = append(m.changed, to)
	return nil
}

func (m *recordingMailer) SendPasswordReset(_ context.Context, mail domain.OTPMail) error {
	m.resets = append(m.resets, mail)
	return nil
}

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
	uow := &fakeUoW{audit: &fakeAudit{}, users: repos.users, challenges: &fakeChallenges{}}
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
