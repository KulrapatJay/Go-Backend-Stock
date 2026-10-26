package service

import (
	"context"
	"errors"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

var _ port.EmailVerificationService = (*Auth)(nil)

// Request implements port.EmailVerificationService. Only an unverified account with a password gets
// a new code (the old one is invalidated in the same transaction); an unknown, verified or
// Google-only email gets the same answer with a decoy challenge ID and nothing is written or sent.
func (a *Auth) Request(ctx context.Context, email domain.Email, meta domain.RequestMeta) (domain.OTPReceipt, error) {
	defer a.pacer.Wait(ctx, a.clock.Now())

	rules, err := a.limits.EmailVerificationRequest(email, meta.ClientIP)
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return domain.OTPReceipt{}, err
	}
	decoy, err := domain.NewID()
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	receipt := domain.OTPReceipt{ChallengeID: decoy, ExpiresIn: otpLifetime}

	creds, err := a.store.Users().CredentialsByEmail(ctx, email)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return receipt, nil
	case err != nil:
		return domain.OTPReceipt{}, err
	case !creds.HasPassword() || creds.User.EmailVerifiedAt != nil:
		return receipt, nil
	}

	now := NowForStorage(a.clock)
	code, challenge, err := a.newVerificationChallenge(creds.User.ID, now)
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	issued := false
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		user, err := tx.Users().Lock(ctx, creds.User.ID)
		if err != nil {
			return err
		}
		if user.EmailVerifiedAt != nil {
			return nil // verified meanwhile: answer like any other verified account
		}
		// The user row lock makes this and the insert atomic against a concurrent request.
		if err := tx.OTPChallenges().InvalidateOpen(ctx, user.ID, domain.OTPPurposeEmailVerification, now); err != nil {
			return err
		}
		if err := tx.OTPChallenges().Insert(ctx, challenge); err != nil {
			return err
		}
		issued = true
		return a.appendAudit(ctx, tx, meta, verificationRequested(user.ID))
	})
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	if !issued {
		return receipt, nil
	}
	a.send(meta, "email_verification", func() error {
		return a.mailer.SendEmailVerification(ctx, domain.OTPMail{To: email, OTP: code, ChallengeID: challenge.ID, ExpiresAt: challenge.ExpiresAt})
	})
	return domain.OTPReceipt{ChallengeID: challenge.ID, ExpiresIn: otpLifetime}, nil
}

// Confirm implements port.EmailVerificationService (phase 1 spec section 9, milestone 4 design
// section 5). A failed attempt is audited and, for a wrong code, counted in the same transaction,
// which commits before the error is returned, so neither is lost with the failure.
func (a *Auth) Confirm(ctx context.Context, in domain.VerifyEmailInput, meta domain.RequestMeta) error {
	rules, err := a.limits.EmailVerificationConfirm(meta.ClientIP)
	if err != nil {
		return err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return err
	}

	found, err := a.store.OTPChallenges().ByID(ctx, domain.OTPPurposeEmailVerification, in.ChallengeID)
	if errors.Is(err, domain.ErrNotFound) {
		// An unknown ID, or one issued for password reset (ByID filters by purpose).
		if err := a.recordFailure(ctx, meta, verificationFailed(in.ChallengeID, "challenge_not_found")); err != nil {
			return err
		}
		return domain.ErrInvalidVerification
	}
	if err != nil {
		return err
	}

	var failure string
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		failure = ""
		// Lock order: user, then challenge — the same as every other use case on these rows.
		if _, err := tx.Users().Lock(ctx, found.UserID); err != nil {
			return err
		}
		c, err := tx.OTPChallenges().Lock(ctx, domain.OTPPurposeEmailVerification, found.ID)
		if err != nil {
			return err
		}
		now := NowForStorage(a.clock)
		switch {
		case c.ConsumedAt != nil || c.InvalidatedAt != nil:
			failure = "challenge_closed"
		case !now.Before(c.ExpiresAt):
			failure = "challenge_expired"
		case c.FailedAttempts >= a.otpMaxAttempts:
			failure = "too_many_attempts"
		case !a.otp.Verify(c.OTPHash, domain.OTPPurposeEmailVerification, c.ID, c.UserID, in.OTP):
			failure = "otp_mismatch"
			if err := tx.OTPChallenges().RecordFailure(ctx, domain.OTPPurposeEmailVerification, c.ID); err != nil {
				return err
			}
		}
		if failure != "" {
			return a.appendAudit(ctx, tx, meta, verificationFailed(c.ID, failure))
		}
		if err := tx.OTPChallenges().Consume(ctx, domain.OTPPurposeEmailVerification, c.ID, now); err != nil {
			return err
		}
		if err := tx.Users().MarkEmailVerified(ctx, c.UserID, now); err != nil {
			return err
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthEmailVerified, Outcome: domain.AuditOutcomeSuccess,
			Actor: c.UserID, TargetType: "user", TargetID: c.UserID.String(),
		})
	})
	if err != nil {
		return err
	}
	if failure != "" {
		return domain.ErrInvalidVerification
	}
	return nil
}

func verificationFailed(challengeID domain.ID, reason string) auditSpec {
	return auditSpec{
		Type: domain.EventAuthEmailVerificationFailed, Outcome: domain.AuditOutcomeFailure,
		TargetType: "otp_challenge", TargetID: challengeID.String(), Reason: reason,
	}
}
