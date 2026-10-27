package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// confirmChallenges stands in for otp_challenges: ByID and Lock answer what the test sets.
type confirmChallenges struct {
	port.OTPChallengeRepository
	byID    func() (domain.OTPChallenge, error)
	lock    func() (domain.OTPChallenge, error)
	failure int
}

func (c *confirmChallenges) ByID(context.Context, domain.OTPPurpose, domain.ID) (domain.OTPChallenge, error) {
	return c.byID()
}

func (c *confirmChallenges) Lock(context.Context, domain.OTPPurpose, domain.ID) (domain.OTPChallenge, error) {
	return c.lock()
}

func (c *confirmChallenges) RecordFailure(context.Context, domain.OTPPurpose, domain.ID) error {
	c.failure++
	return nil
}

// confirmUsers locks any user.
type confirmUsers struct{ port.UserRepository }

func (confirmUsers) Lock(_ context.Context, id domain.ID) (domain.User, error) {
	return domain.User{ID: id}, nil
}

type confirmTx struct {
	port.TxRepositories
	audit      *fakeAudit
	challenges *confirmChallenges
}

func (t confirmTx) Audit() port.AuditRepository                { return t.audit }
func (t confirmTx) OTPChallenges() port.OTPChallengeRepository { return t.challenges }
func (confirmTx) Users() port.UserRepository                   { return confirmUsers{} }

type confirmUoW struct {
	audit      *fakeAudit
	challenges *confirmChallenges
	commits    int
}

func (u *confirmUoW) WithinTransaction(_ context.Context, fn func(port.TxRepositories) error) error {
	if err := fn(confirmTx{audit: u.audit, challenges: u.challenges}); err != nil {
		return err
	}
	u.commits++
	return nil
}

// confirmRepos exposes the same challenge fake outside a transaction, for the ByID read.
type confirmRepos struct {
	*fakeRepositories
	challenges *confirmChallenges
}

func (r confirmRepos) OTPChallenges() port.OTPChallengeRepository { return r.challenges }

func newConfirmAuth(t *testing.T, challenges *confirmChallenges, mutate func(*AuthDeps)) (*Auth, *confirmUoW) {
	t.Helper()
	uow := &confirmUoW{audit: &fakeAudit{}, challenges: challenges}
	ta := newTestAuth(t, func(d *AuthDeps) {
		d.Store = confirmRepos{fakeRepositories: d.Store.(*fakeRepositories), challenges: challenges}
		d.UnitOfWork = uow
		if mutate != nil {
			mutate(d)
		}
	})
	return ta.auth, uow
}

func confirmInput(t *testing.T) domain.VerifyEmailInput {
	t.Helper()
	otp, err := domain.ParseOTP("123456")
	if err != nil {
		t.Fatal(err)
	}
	return domain.VerifyEmailInput{ChallengeID: mustID(t), OTP: otp}
}

func TestConfirmWaitsForTheResponseFloorOnEveryPath(t *testing.T) {
	missing := func() (domain.OTPChallenge, error) { return domain.OTPChallenge{}, domain.ErrNotFound }
	open := func() (domain.OTPChallenge, error) {
		return domain.OTPChallenge{ID: mustID(t), UserID: mustID(t), ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	cases := map[string]*confirmChallenges{
		"unknown challenge (decoy)": {byID: missing},
		"real challenge":            {byID: open, lock: open},
	}
	for name, challenges := range cases {
		t.Run(name, func(t *testing.T) {
			var slept []time.Duration
			auth, _ := newConfirmAuth(t, challenges, func(d *AuthDeps) {
				d.Pacer = &Pacer{
					clock: d.Clock, floor: 300 * time.Millisecond,
					sleep:        func(_ context.Context, d time.Duration) { slept = append(slept, d) },
					randomJitter: func(time.Duration) time.Duration { return 0 },
				}
			})
			if err := auth.Confirm(t.Context(), confirmInput(t), testMeta(t)); !errors.Is(err, domain.ErrInvalidVerification) {
				t.Fatalf("Confirm() error = %v, want ErrInvalidVerification", err)
			}
			if len(slept) != 1 || slept[0] != 300*time.Millisecond {
				t.Errorf("pacer slept %v, want one wait of the 300ms floor", slept)
			}
		})
	}
}

func TestConfirmLockMissAfterByIDIsAnAuditedInvalidVerification(t *testing.T) {
	challenges := &confirmChallenges{
		byID: func() (domain.OTPChallenge, error) { return domain.OTPChallenge{ID: mustID(t), UserID: mustID(t)}, nil },
		lock: func() (domain.OTPChallenge, error) { return domain.OTPChallenge{}, domain.ErrNotFound },
	}
	auth, uow := newConfirmAuth(t, challenges, nil)

	err := auth.Confirm(t.Context(), confirmInput(t), testMeta(t))
	if !errors.Is(err, domain.ErrInvalidVerification) {
		t.Fatalf("Confirm() error = %v, want ErrInvalidVerification (not a raw not-found)", err)
	}
	events := uow.audit.events
	if len(events) != 1 || events[0].EventType != domain.EventAuthEmailVerificationFailed || events[0].ReasonCode != "challenge_not_found" {
		t.Errorf("audit = %+v, want one auth.email_verification_failed with reason challenge_not_found", events)
	}
	if uow.commits != 1 {
		t.Errorf("commits = %d, want 1: the audit must be committed", uow.commits)
	}
}
