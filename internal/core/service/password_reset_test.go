package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
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
