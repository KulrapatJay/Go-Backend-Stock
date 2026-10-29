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

// Reset is a placeholder until plan 5 task 4 implements it; it only keeps PasswordReset a
// port.PasswordResetService. Nothing routes to it yet.
func (r *PasswordReset) Reset(context.Context, domain.ResetPasswordInput, domain.RequestMeta) error {
	return errors.New("not implemented")
}
