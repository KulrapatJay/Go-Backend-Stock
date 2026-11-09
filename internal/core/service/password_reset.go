package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

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

	// Hash before looking the challenge up, so a decoy or unknown ID pays the same cost and can
	// get the same server_busy as a real one: a busy hasher must not show whether the account
	// exists. The hashes wasted on decoys are bounded by the per-IP limit above. Never inside a
	// transaction: Argon2id is slow and holds memory.
	hash, err := a.hasher.Hash(ctx, in.NewPassword)
	if err != nil {
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
	// The email feeds the password rule and the notice; read outside the transaction.
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
		if failure, err = a.checkResetCode(ctx, tx, c, in.OTP, now); err != nil {
			return err
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
		return a.finishReset(ctx, tx, meta, c.UserID, hash, now)
	})
	if err != nil {
		return err
	}
	return a.resetOutcome(ctx, meta, user.Email, failure, weak)
}

// Verify implements port.PasswordResetService: the first step of the two-step reset (ADR-0022).
// The code is checked exactly as Reset checks it, with the same refusals, counting and audit; a
// right code is spent by storing a grant, and only the grant's token can finish the reset. The
// challenge stays open, so asking for a new code invalidates the token with it.
func (r *PasswordReset) Verify(ctx context.Context, in domain.VerifyResetInput, meta domain.RequestMeta) (domain.ResetGrantReceipt, error) {
	a := r.a
	// A decoy challenge ID and a real one cost different amounts of work: the floor hides which.
	defer a.pacer.Wait(ctx, a.clock.Now())

	// The same quota as Reset: one budget of code guesses per IP, whichever step makes them.
	rules, err := a.limits.PasswordResetConfirm(meta.ClientIP)
	if err != nil {
		return domain.ResetGrantReceipt{}, err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return domain.ResetGrantReceipt{}, err
	}
	binding := in.BrowserBinding.Reveal()
	if binding == "" {
		return domain.ResetGrantReceipt{}, domain.NewError(domain.CodeInternal, "reset verify needs the CSRF binding")
	}
	token, tokenHash, err := NewResetToken()
	if err != nil {
		return domain.ResetGrantReceipt{}, err
	}

	found, err := a.store.OTPChallenges().ByID(ctx, domain.OTPPurposePasswordReset, in.ChallengeID)
	if errors.Is(err, domain.ErrNotFound) {
		if err := a.recordFailure(ctx, meta, resetFailed(in.ChallengeID, "challenge_not_found")); err != nil {
			return domain.ResetGrantReceipt{}, err
		}
		return domain.ResetGrantReceipt{}, domain.ErrInvalidReset
	}
	if err != nil {
		return domain.ResetGrantReceipt{}, err
	}

	var failure string
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		failure = ""
		// The same lock order as Reset and Request: user, then challenge.
		if _, err := tx.Users().Lock(ctx, found.UserID); err != nil {
			return err
		}
		c, err := tx.OTPChallenges().Lock(ctx, domain.OTPPurposePasswordReset, found.ID)
		if errors.Is(err, domain.ErrNotFound) {
			failure = "challenge_not_found"
			return a.appendAudit(ctx, tx, meta, resetFailed(found.ID, failure))
		}
		if err != nil {
			return err
		}
		now := NowForStorage(a.clock)
		if failure, err = a.checkResetCode(ctx, tx, c, in.OTP, now); err != nil {
			return err
		}
		if failure != "" {
			return a.appendAudit(ctx, tx, meta, resetFailed(c.ID, failure))
		}
		grant, err := domain.NewResetGrant(domain.ResetGrant{
			TokenHash: tokenHash, BindingHash: HashResetBinding(binding),
			VerifiedAt: now, ExpiresAt: now.Add(resetTokenLifetime),
		})
		if err != nil {
			return err
		}
		if err := tx.OTPChallenges().GrantReset(ctx, c.ID, grant); err != nil {
			return err
		}
		// The right code proves the caller reads the account's mailbox: the actor is its owner.
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthPasswordResetVerified, Outcome: domain.AuditOutcomeSuccess,
			Actor: c.UserID, TargetType: "otp_challenge", TargetID: c.ID.String(),
		})
	})
	if err != nil {
		return domain.ResetGrantReceipt{}, err
	}
	if failure != "" {
		return domain.ResetGrantReceipt{}, domain.ErrInvalidReset
	}
	return domain.ResetGrantReceipt{Token: domain.NewSecretText(token), ExpiresIn: resetTokenLifetime}, nil
}

// ResetWithToken implements port.PasswordResetService: the second step of the two-step reset. It
// is Reset with the token of a verified code in place of the code. A refused token is audited as
// auth.password_reset_failed in a transaction that commits before the error returns; a password
// that contains the email spends nothing, so the owner can pick another within the token's life.
func (r *PasswordReset) ResetWithToken(ctx context.Context, in domain.ResetWithTokenInput, meta domain.RequestMeta) error {
	a := r.a
	defer a.pacer.Wait(ctx, a.clock.Now())

	rules, err := a.limits.PasswordResetConfirm(meta.ClientIP)
	if err != nil {
		return err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return err
	}
	binding := in.BrowserBinding.Reveal()
	if binding == "" {
		return domain.NewError(domain.CodeInternal, "reset confirm needs the CSRF binding")
	}
	// As in Reset: hash before any lookup and outside the transaction, so an unknown token costs
	// the same as a real one and a busy hasher cannot tell them apart.
	hash, err := a.hasher.Hash(ctx, in.NewPassword)
	if err != nil {
		return err
	}

	tokenHash, ok := HashResetToken(in.Token.Reveal())
	if !ok {
		if err := a.recordFailure(ctx, meta, tokenFailed(domain.ID{}, "reset_token_not_found")); err != nil {
			return err
		}
		return domain.ErrInvalidReset
	}
	found, err := a.store.OTPChallenges().ByResetTokenHash(ctx, tokenHash)
	if errors.Is(err, domain.ErrNotFound) {
		if err := a.recordFailure(ctx, meta, tokenFailed(domain.ID{}, "reset_token_not_found")); err != nil {
			return err
		}
		return domain.ErrInvalidReset
	}
	if err != nil {
		return err
	}
	user, err := a.store.Users().ByID(ctx, found.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		if err := a.recordFailure(ctx, meta, tokenFailed(found.ID, "reset_token_not_found")); err != nil {
			return err
		}
		return domain.ErrInvalidReset
	}
	if err != nil {
		return err
	}

	var failure string
	var weak bool
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		failure, weak = "", false
		if _, err := tx.Users().Lock(ctx, found.UserID); err != nil {
			return err
		}
		c, err := tx.OTPChallenges().Lock(ctx, domain.OTPPurposePasswordReset, found.ID)
		if errors.Is(err, domain.ErrNotFound) {
			failure = "reset_token_not_found"
			return a.appendAudit(ctx, tx, meta, tokenFailed(found.ID, failure))
		}
		if err != nil {
			return err
		}
		now := NowForStorage(a.clock)
		switch {
		case c.Grant == nil || subtle.ConstantTimeCompare(c.Grant.TokenHash, tokenHash) != 1:
			failure = "reset_token_not_found"
		case c.ConsumedAt != nil || c.InvalidatedAt != nil:
			failure = "challenge_closed" // used, or a newer code was requested
		case !now.Before(c.Grant.ExpiresAt):
			failure = "reset_token_expired"
		case subtle.ConstantTimeCompare(c.Grant.BindingHash, HashResetBinding(binding)) != 1:
			failure = "browser_mismatch"
		}
		if failure != "" {
			return a.appendAudit(ctx, tx, meta, tokenFailed(c.ID, failure))
		}
		if domain.PasswordContainsEmail(in.NewPassword, user.Email) {
			weak = true
			return nil
		}
		if err := tx.OTPChallenges().ConsumeResetGrant(ctx, c.ID, now); err != nil {
			return err
		}
		return a.finishReset(ctx, tx, meta, c.UserID, hash, now)
	})
	if err != nil {
		return err
	}
	return a.resetOutcome(ctx, meta, user.Email, failure, weak)
}

// checkResetCode judges a locked password reset challenge against a code, counting a wrong one. It
// returns the audit reason of a refusal, or "" for a right code. A challenge whose code was already
// verified by the two-step flow is closed to codes: only its token can finish it.
func (a *Auth) checkResetCode(ctx context.Context, tx port.TxRepositories, c domain.OTPChallenge, code domain.OTPCode, now time.Time) (string, error) {
	switch {
	case c.ConsumedAt != nil || c.InvalidatedAt != nil || c.Grant != nil:
		return "challenge_closed", nil
	case !now.Before(c.ExpiresAt):
		return "challenge_expired", nil
	case c.FailedAttempts >= a.otpMaxAttempts:
		return "too_many_attempts", nil
	case !a.otp.Verify(c.OTPHash, domain.OTPPurposePasswordReset, c.ID, c.UserID, code):
		if err := tx.OTPChallenges().RecordFailure(ctx, domain.OTPPurposePasswordReset, c.ID); err != nil {
			return "", err
		}
		return "otp_mismatch", nil
	}
	return "", nil
}

// finishReset is what every successful reset writes after it spent its code or token: the new
// password, the verified email (its owner just proved it), the closed verification code and the
// revoked sessions, with their audit events, in the caller's transaction.
func (a *Auth) finishReset(ctx context.Context, tx port.TxRepositories, meta domain.RequestMeta, userID domain.ID, hash domain.PasswordHash, now time.Time) error {
	if err := tx.Users().SetPassword(ctx, userID, hash, now); err != nil {
		return err
	}
	// Receiving the code proves the owner reads this mailbox, so an account someone else
	// registered first is taken back, and its pending verification code stops working.
	if err := tx.Users().MarkEmailVerified(ctx, userID, now); err != nil {
		return err
	}
	if err := tx.OTPChallenges().InvalidateOpen(ctx, userID, domain.OTPPurposeEmailVerification, now); err != nil {
		return err
	}
	revoked, err := tx.Sessions().RevokeAll(ctx, userID, now, domain.RevokeReasonPasswordReset)
	if err != nil {
		return err
	}
	if err := a.appendAudit(ctx, tx, meta, auditSpec{
		Type: domain.EventAuthPasswordResetSucceeded, Outcome: domain.AuditOutcomeSuccess,
		Actor: userID, TargetType: "user", TargetID: userID.String(),
	}); err != nil {
		return err
	}
	if revoked == 0 {
		return nil
	}
	return a.appendAudit(ctx, tx, meta, auditSpec{
		Type: domain.EventAuthSessionsRevoked, Outcome: domain.AuditOutcomeSuccess,
		Actor: userID, TargetType: "user", TargetID: userID.String(), Reason: string(domain.RevokeReasonPasswordReset),
	})
}

// resetOutcome turns the result of a committed reset transaction into the caller's answer, and
// sends the password-changed notice after a success.
func (a *Auth) resetOutcome(ctx context.Context, meta domain.RequestMeta, email domain.Email, failure string, weak bool) error {
	switch {
	case failure != "":
		return domain.ErrInvalidReset
	case weak:
		return domain.NewReasonsError(domain.CodeInvalidInput, "password does not meet the requirements",
			string(domain.PasswordReasonContainsEmail))
	}
	a.send(meta, "password_changed", func() error { return a.mailer.SendPasswordChanged(ctx, email) })
	return nil
}

func resetFailed(challengeID domain.ID, reason string) auditSpec {
	return auditSpec{
		Type: domain.EventAuthOTPVerificationFailed, Outcome: domain.AuditOutcomeFailure,
		TargetType: "otp_challenge", TargetID: challengeID.String(), Reason: reason,
	}
}

// tokenFailed is the audit of a refused reset token. The target is the challenge when the token
// led to one, and the reset flow itself otherwise.
func tokenFailed(challengeID domain.ID, reason string) auditSpec {
	s := auditSpec{
		Type: domain.EventAuthPasswordResetFailed, Outcome: domain.AuditOutcomeFailure,
		TargetType: "otp_challenge", Reason: reason,
	}
	if challengeID != (domain.ID{}) {
		s.TargetID = challengeID.String()
	} else {
		s.TargetType = "password_reset"
	}
	return s
}
