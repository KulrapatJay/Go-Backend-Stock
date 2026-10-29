package service

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

func TestResetRequestWaitsForTheResponseFloorOnEveryPath(t *testing.T) {
	withPassword := func(creds domain.UserCredentials) domain.UserCredentials {
		creds.PasswordHash = "hash"
		return creds
	}
	user := domain.UserCredentials{User: domain.User{ID: mustID(t), Email: mustEmail(t, "known@example.com")}}
	cases := map[string]map[string]domain.UserCredentials{
		"unknown email":       {},
		"google-only":         {"known@example.com": user},
		"account w/ password": {"known@example.com": withPassword(user)},
	}
	for name, credentials := range cases {
		t.Run(name, func(t *testing.T) {
			var slept []time.Duration
			mailer := &recordingMailer{}
			ta := newTestAuth(t, func(d *AuthDeps) {
				d.Mailer = mailer
				d.Pacer = &Pacer{
					clock: d.Clock, floor: 300 * time.Millisecond,
					sleep:        func(_ context.Context, d time.Duration) { slept = append(slept, d) },
					randomJitter: func(time.Duration) time.Duration { return 0 },
				}
			})
			ta.repos.users.credentials = credentials

			receipt, err := ta.auth.PasswordReset().Request(t.Context(), mustEmail(t, "known@example.com"), testMeta(t))
			if err != nil {
				t.Fatalf("Request() error = %v", err)
			}
			if receipt.ChallengeID == (domain.ID{}) || receipt.ExpiresIn != otpLifetime {
				t.Errorf("receipt = %+v, want a challenge ID and the 10 minute lifetime", receipt)
			}
			if len(slept) != 1 || slept[0] != 300*time.Millisecond {
				t.Errorf("pacer slept %v, want one wait of the 300ms floor", slept)
			}
			if wantMails := map[bool]int{true: 1, false: 0}[name == "account w/ password"]; len(mailer.resets) != wantMails {
				t.Errorf("%d reset emails, want %d", len(mailer.resets), wantMails)
			}
		})
	}
}

func TestResetRequestRateLimitedWritesNothing(t *testing.T) {
	ta := newTestAuth(t, nil)
	ta.repos.limits.decision = domain.RateLimitDecision{Allowed: false, RetryAfter: time.Minute}
	ta.repos.users = nil // reading users after the refusal would dereference nil and panic

	_, err := ta.auth.PasswordReset().Request(t.Context(), mustEmail(t, "user@example.com"), testMeta(t))
	if !errors.Is(err, domain.NewError(domain.CodeRateLimited, "")) {
		t.Fatalf("Request() error = %v, want rate_limited", err)
	}
	if ta.uow.runs != 0 {
		t.Errorf("%d transactions ran, want none: a refused request writes nothing", ta.uow.runs)
	}
}

// resetChallenges adds what a successful reset writes to confirmChallenges.
type resetChallenges struct {
	confirmChallenges
	consumed    int
	invalidated []domain.OTPPurpose
}

func (c *resetChallenges) Consume(context.Context, domain.OTPPurpose, domain.ID, time.Time) error {
	c.consumed++
	return nil
}

func (c *resetChallenges) InvalidateOpen(_ context.Context, _ domain.ID, purpose domain.OTPPurpose, _ time.Time) error {
	c.invalidated = append(c.invalidated, purpose)
	return nil
}

// resetUsers answers ByID and Lock with one user and records the password it is given.
type resetUsers struct {
	port.UserRepository
	user      domain.User
	newHash   domain.PasswordHash
	verifieds int
}

func (u *resetUsers) ByID(context.Context, domain.ID) (domain.User, error) { return u.user, nil }
func (u *resetUsers) Lock(context.Context, domain.ID) (domain.User, error) { return u.user, nil }

func (u *resetUsers) SetPassword(_ context.Context, _ domain.ID, hash domain.PasswordHash, _ time.Time) error {
	u.newHash = hash
	return nil
}

func (u *resetUsers) MarkEmailVerified(context.Context, domain.ID, time.Time) error {
	u.verifieds++
	return nil
}

type resetSessions struct {
	port.SessionRepository
	revoked int64
}

func (s resetSessions) RevokeAll(context.Context, domain.ID, time.Time, domain.RevokeReason) (int64, error) {
	return s.revoked, nil
}

type resetTx struct {
	port.TxRepositories
	audit      *fakeAudit
	challenges *resetChallenges
	users      *resetUsers
	sessions   resetSessions
}

func (t resetTx) Audit() port.AuditRepository                { return t.audit }
func (t resetTx) OTPChallenges() port.OTPChallengeRepository { return t.challenges }
func (t resetTx) Users() port.UserRepository                 { return t.users }
func (t resetTx) Sessions() port.SessionRepository           { return t.sessions }

// resetUoW runs the callback like a real transaction and lets a test act when one starts and ends.
type resetUoW struct {
	tx      resetTx
	commits int
	enter   func()
	leave   func()
}

func (u *resetUoW) WithinTransaction(_ context.Context, fn func(port.TxRepositories) error) error {
	if u.enter != nil {
		u.enter()
	}
	if u.leave != nil {
		defer u.leave()
	}
	if err := fn(u.tx); err != nil {
		return err
	}
	u.commits++
	return nil
}

// resetRepos exposes the same fakes outside a transaction, for the reads before it.
type resetRepos struct {
	*fakeRepositories
	challenges *resetChallenges
	users      *resetUsers
}

func (r resetRepos) OTPChallenges() port.OTPChallengeRepository { return r.challenges }
func (r resetRepos) Users() port.UserRepository                 { return r.users }

type resetFixture struct {
	auth       *Auth
	uow        *resetUoW
	challenges *resetChallenges
	users      *resetUsers
	mailer     *recordingMailer
	clock      *fakeClock
}

func newResetFixture(t *testing.T, challenges *resetChallenges, mutate func(*AuthDeps)) *resetFixture {
	t.Helper()
	users := &resetUsers{user: domain.User{ID: mustID(t), Email: mustEmail(t, "owner@example.com")}}
	f := &resetFixture{challenges: challenges, users: users, mailer: &recordingMailer{}}
	f.uow = &resetUoW{tx: resetTx{audit: &fakeAudit{}, challenges: challenges, users: users}}
	ta := newTestAuth(t, func(d *AuthDeps) {
		d.Store = resetRepos{fakeRepositories: d.Store.(*fakeRepositories), challenges: challenges, users: users}
		d.UnitOfWork = f.uow
		d.Mailer = f.mailer
		f.clock = d.Clock.(*fakeClock)
		if mutate != nil {
			mutate(d)
		}
	})
	f.auth = ta.auth
	return f
}

func resetInput(t *testing.T, otp string) domain.ResetPasswordInput {
	t.Helper()
	code, err := domain.ParseOTP(otp)
	if err != nil {
		t.Fatal(err)
	}
	return domain.ResetPasswordInput{ChallengeID: mustID(t), OTP: code, NewPassword: mustPassword(t, "Nw7#pLq2")}
}

func TestResetWaitsForTheResponseFloorOnEveryPath(t *testing.T) {
	missing := func() (domain.OTPChallenge, error) { return domain.OTPChallenge{}, domain.ErrNotFound }
	open := func() (domain.OTPChallenge, error) {
		return domain.OTPChallenge{ID: mustID(t), UserID: mustID(t), ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	cases := map[string]*resetChallenges{
		"unknown challenge (decoy)": {confirmChallenges: confirmChallenges{byID: missing}},
		"real challenge":            {confirmChallenges: confirmChallenges{byID: open, lock: open}},
	}
	for name, challenges := range cases {
		t.Run(name, func(t *testing.T) {
			var slept []time.Duration
			f := newResetFixture(t, challenges, func(d *AuthDeps) {
				d.Pacer = &Pacer{
					clock: d.Clock, floor: 300 * time.Millisecond,
					sleep:        func(_ context.Context, d time.Duration) { slept = append(slept, d) },
					randomJitter: func(time.Duration) time.Duration { return 0 },
				}
			})
			if err := f.auth.PasswordReset().Reset(t.Context(), resetInput(t, "123456"), testMeta(t)); !errors.Is(err, domain.ErrInvalidReset) {
				t.Fatalf("Reset() error = %v, want ErrInvalidReset", err)
			}
			if len(slept) != 1 || slept[0] != 300*time.Millisecond {
				t.Errorf("pacer slept %v, want one wait of the 300ms floor", slept)
			}
		})
	}
}

func TestResetLockMissAfterByIDIsAnAuditedInvalidReset(t *testing.T) {
	challenges := &resetChallenges{confirmChallenges: confirmChallenges{
		byID: func() (domain.OTPChallenge, error) { return domain.OTPChallenge{ID: mustID(t), UserID: mustID(t)}, nil },
		lock: func() (domain.OTPChallenge, error) { return domain.OTPChallenge{}, domain.ErrNotFound },
	}}
	f := newResetFixture(t, challenges, nil)

	err := f.auth.PasswordReset().Reset(t.Context(), resetInput(t, "123456"), testMeta(t))
	if !errors.Is(err, domain.ErrInvalidReset) {
		t.Fatalf("Reset() error = %v, want ErrInvalidReset (not a raw not-found)", err)
	}
	events := f.uow.tx.audit.events
	if len(events) != 1 || events[0].EventType != domain.EventAuthOTPVerificationFailed || events[0].ReasonCode != "challenge_not_found" {
		t.Errorf("audit = %+v, want one auth.otp_verification_failed with reason challenge_not_found", events)
	}
	if f.uow.commits != 1 {
		t.Errorf("commits = %d, want 1: the audit must be committed", f.uow.commits)
	}
	if f.users.newHash != "" || f.mailer.changed != nil {
		t.Error("a refused reset must change nothing and notify no one")
	}
}

// Argon2id is slow and holds memory: Reset must hash before it takes the user and challenge row
// locks. The test makes the only hashing slot unavailable while the transaction runs, so a hash
// attempted inside it would fail with server_busy.
func TestResetHashesBeforeTheTransaction(t *testing.T) {
	hasher, _ := newHasher(t, testArgon2Params, 1, 20*time.Millisecond)
	userID, challengeID := mustID(t), mustID(t)
	var f *resetFixture
	challenge := func() (domain.OTPChallenge, error) {
		return domain.OTPChallenge{
			ID: challengeID, UserID: userID, Purpose: domain.OTPPurposePasswordReset,
			OTPHash:   newTestOTPCodec(t).Digest(domain.OTPPurposePasswordReset, challengeID, userID, mustOTP(t, "123456")),
			ExpiresAt: f.clock.Now().Add(10 * time.Minute),
		}, nil
	}
	f = newResetFixture(t, &resetChallenges{confirmChallenges: confirmChallenges{byID: challenge, lock: challenge}},
		func(d *AuthDeps) { d.Hasher = hasher })
	f.uow.enter = func() { hasher.slots <- struct{}{} }
	f.uow.leave = func() { <-hasher.slots }

	in := resetInput(t, "123456")
	in.ChallengeID = challengeID
	if err := f.auth.PasswordReset().Reset(t.Context(), in, testMeta(t)); err != nil {
		t.Fatalf("Reset() error = %v, want success: the hash must be made before the transaction", err)
	}
	if f.users.newHash == "" {
		t.Error("the new password hash must be stored")
	}
	if f.challenges.consumed != 1 || f.users.verifieds != 1 || !slices.Equal(f.challenges.invalidated, []domain.OTPPurpose{domain.OTPPurposeEmailVerification}) {
		t.Errorf("consumed=%d verifieds=%d invalidated=%v", f.challenges.consumed, f.users.verifieds, f.challenges.invalidated)
	}
	if len(f.mailer.changed) != 1 {
		t.Errorf("%d password-changed notices, want 1", len(f.mailer.changed))
	}
}

// newTestOTPCodec is the codec newTestAuth gives Auth, so a test can build a matching digest.
func newTestOTPCodec(t *testing.T) *OTPCodec {
	t.Helper()
	codec, err := NewOTPCodec(bytes.Repeat([]byte("o"), 32))
	if err != nil {
		t.Fatal(err)
	}
	return codec
}
