package service

import (
	"context"
	"errors"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PasswordReset implements port.PasswordResetService on top of the dependencies of Auth. It is a
// separate type because Auth already has Request and Confirm for email verification.
type PasswordReset struct{ a *Auth }

var _ port.PasswordResetService = (*PasswordReset)(nil)

// PasswordReset returns the password reset use cases of a.
func (a *Auth) PasswordReset() *PasswordReset { return &PasswordReset{a: a} }

// Request implements port.PasswordResetService. Only an account with a password gets a code (the
// old one is invalidated in the same transaction); an unknown or Google-only email gets the same
// answer with a decoy challenge ID and nothing is written or sent.
func (r *PasswordReset) Request(ctx context.Context, email domain.Email, meta domain.RequestMeta) (domain.OTPReceipt, error) {
	a := r.a
	// All paths cost the same to the caller, so the time does not show whether the account exists.
	defer a.pacer.Wait(ctx, a.clock.Now())

	rules, err := a.limits.PasswordResetRequest(email, meta.ClientIP)
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
	case !creds.HasPassword():
		return receipt, nil // Google-only: there is no local password to reset
	}

	now := NowForStorage(a.clock)
	code, challenge, err := a.newOTPChallenge(domain.OTPPurposePasswordReset, creds.User.ID, now)
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		user, err := tx.Users().Lock(ctx, creds.User.ID)
		if err != nil {
			return err
		}
		// The user row lock makes this and the insert atomic against a concurrent request.
		if err := tx.OTPChallenges().InvalidateOpen(ctx, user.ID, domain.OTPPurposePasswordReset, now); err != nil {
			return err
		}
		if err := tx.OTPChallenges().Insert(ctx, challenge); err != nil {
			return err
		}
		// The actor stays NULL: the request proved nothing about who asked.
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthPasswordResetRequested, Outcome: domain.AuditOutcomeSuccess,
			TargetType: "user", TargetID: user.ID.String(),
		})
	})
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	a.send(meta, "password_reset", func() error {
		return a.mailer.SendPasswordReset(ctx, domain.OTPMail{To: email, OTP: code, ChallengeID: challenge.ID, ExpiresAt: challenge.ExpiresAt})
	})
	return domain.OTPReceipt{ChallengeID: challenge.ID, ExpiresIn: otpLifetime}, nil
}

// Reset implements port.PasswordResetService. The code is checked before the new password is
// judged against the account's email, so a caller who guesses a challenge ID learns nothing about
// the account from a password rule. A failed attempt is audited and, for a wrong code, counted in
// the same transaction, which commits before the error is returned. A success replaces the
// password, verifies the email (its owner just proved it), closes the open verification code and
// revokes every session, all in one transaction with their audit events.
func (r *PasswordReset) Reset(ctx context.Context, in domain.ResetPasswordInput, meta domain.RequestMeta) error {
	a := r.a
	// A decoy challenge ID and a real one cost different amounts of work: the floor hides which.
	defer a.pacer.Wait(ctx, a.clock.Now())

	rules, err := a.limits.PasswordResetConfirm(meta.ClientIP)
	if err != nil {
		return err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return err
	}

	found, err := a.store.OTPChallenges().ByID(ctx, domain.OTPPurposePasswordReset, in.ChallengeID)
	if errors.Is(err, domain.ErrNotFound) {
		// An unknown or decoy ID, or one issued for email verification (ByID filters by purpose).
		if err := a.recordFailure(ctx, meta, resetFailed(in.ChallengeID, "challenge_not_found")); err != nil {
			return err
		}
		return domain.ErrInvalidReset
	}
	if err != nil {
		return err
	}
	// The email feeds the password rule and the notice. Both reads and the slow hash stay outside
	// the transaction so no row lock is held while Argon2id runs.
	user, err := a.store.Users().ByID(ctx, found.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		if err := a.recordFailure(ctx, meta, resetFailed(found.ID, "challenge_not_found")); err != nil {
			return err
		}
		return domain.ErrInvalidReset
	}
	if err != nil {
		return err
	}
	hash, err := a.hasher.Hash(ctx, in.NewPassword)
	if err != nil {
		return err
	}

	var failure string
	var weak bool
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		// A transaction may run again: start from a clean result.
		failure, weak = "", false
		// Lock order: user, then challenge — the same as every other use case on these rows.
		if _, err := tx.Users().Lock(ctx, found.UserID); err != nil {
			return err
		}
		c, err := tx.OTPChallenges().Lock(ctx, domain.OTPPurposePasswordReset, found.ID)
		if errors.Is(err, domain.ErrNotFound) {
			// Deleted between ByID and Lock: the same refusal as an unknown ID, audited and committed.
			failure = "challenge_not_found"
			return a.appendAudit(ctx, tx, meta, resetFailed(found.ID, failure))
		}
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
		case !a.otp.Verify(c.OTPHash, domain.OTPPurposePasswordReset, c.ID, c.UserID, in.OTP):
			failure = "otp_mismatch"
			if err := tx.OTPChallenges().RecordFailure(ctx, domain.OTPPurposePasswordReset, c.ID); err != nil {
				return err
			}
		}
		if failure != "" {
			return a.appendAudit(ctx, tx, meta, resetFailed(c.ID, failure))
		}
		if domain.PasswordContainsEmail(in.NewPassword, user.Email) {
			// The code was right, so this is the owner: tell them, but write nothing and spend
			// neither the code nor an attempt, so they can choose another password.
			weak = true
			return nil
		}
		if err := tx.OTPChallenges().Consume(ctx, domain.OTPPurposePasswordReset, c.ID, now); err != nil {
			return err
		}
		if err := tx.Users().SetPassword(ctx, c.UserID, hash, now); err != nil {
			return err
		}
		// Receiving the code proves the owner reads this mailbox, so an account someone else
		// registered first is taken back, and its pending verification code stops working.
		if err := tx.Users().MarkEmailVerified(ctx, c.UserID, now); err != nil {
			return err
		}
		if err := tx.OTPChallenges().InvalidateOpen(ctx, c.UserID, domain.OTPPurposeEmailVerification, now); err != nil {
			return err
		}
		revoked, err := tx.Sessions().RevokeAll(ctx, c.UserID, now, domain.RevokeReasonPasswordReset)
		if err != nil {
			return err
		}
		if err := a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthPasswordResetSucceeded, Outcome: domain.AuditOutcomeSuccess,
			Actor: c.UserID, TargetType: "user", TargetID: c.UserID.String(),
		}); err != nil {
			return err
		}
		if revoked == 0 {
			return nil
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthSessionsRevoked, Outcome: domain.AuditOutcomeSuccess,
			Actor: c.UserID, TargetType: "user", TargetID: c.UserID.String(), Reason: string(domain.RevokeReasonPasswordReset),
		})
	})
	if err != nil {
		return err
	}
	switch {
	case failure != "":
		return domain.ErrInvalidReset
	case weak:
		return domain.NewReasonsError(domain.CodeInvalidInput, "password does not meet the requirements",
			string(domain.PasswordReasonContainsEmail))
	}
	a.send(meta, "password_changed", func() error { return a.mailer.SendPasswordChanged(ctx, user.Email) })
	return nil
}

func resetFailed(challengeID domain.ID, reason string) auditSpec {
	return auditSpec{
		Type: domain.EventAuthOTPVerificationFailed, Outcome: domain.AuditOutcomeFailure,
		TargetType: "otp_challenge", TargetID: challengeID.String(), Reason: reason,
	}
}
