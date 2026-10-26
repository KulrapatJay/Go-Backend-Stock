package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// otpLifetime is how long a verification code stays valid (phase 1 spec section 10).
const otpLifetime = 10 * time.Minute

// Register implements port.AuthService. A new email gets an unverified account, a challenge and a
// code. An email that already has an account (verified or not, Google-only included) changes
// nothing and gets a notice instead. Both paths hash the password and take the same time (Pacer),
// so the response does not reveal which it was (ADR-0006, ADR-0014).
func (a *Auth) Register(ctx context.Context, in domain.RegisterInput, meta domain.RequestMeta) (domain.OTPReceipt, error) {
	defer a.pacer.Wait(ctx, a.clock.Now())

	rules, err := a.limits.Signup(in.Email, meta.ClientIP)
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return domain.OTPReceipt{}, err
	}
	hash, err := a.hasher.Hash(ctx, in.Password)
	if err != nil {
		return domain.OTPReceipt{}, err
	}

	now := NowForStorage(a.clock)
	userID, err := domain.NewID()
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	code, challenge, err := a.newVerificationChallenge(userID, now)
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	user := domain.User{ID: userID, Email: in.Email, CreatedAt: now, UpdatedAt: now}

	existing := false
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		if err := tx.Users().Insert(ctx, user); err != nil {
			if errors.Is(err, domain.ErrIdentityConflict) {
				existing = true // nothing is written for an account that already exists
				return nil
			}
			return err
		}
		if err := tx.Users().SetPassword(ctx, user.ID, hash, now); err != nil {
			return err
		}
		if err := tx.OTPChallenges().Insert(ctx, challenge); err != nil {
			return err
		}
		if err := a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthRegistered, Outcome: domain.AuditOutcomeSuccess,
			TargetType: "user", TargetID: user.ID.String(), Method: "password",
		}); err != nil {
			return err
		}
		return a.appendAudit(ctx, tx, meta, verificationRequested(user.ID))
	})
	if err != nil {
		return domain.OTPReceipt{}, err
	}

	if existing {
		a.send(meta, "registration_attempt", func() error { return a.mailer.SendRegistrationAttempt(ctx, in.Email) })
	} else {
		a.send(meta, "email_verification", func() error {
			return a.mailer.SendEmailVerification(ctx, domain.OTPMail{To: in.Email, OTP: code, ChallengeID: challenge.ID, ExpiresAt: challenge.ExpiresAt})
		})
	}
	// For an existing account the challenge was never stored: its ID is a decoy of the same shape.
	return domain.OTPReceipt{ChallengeID: challenge.ID, ExpiresIn: otpLifetime}, nil
}

// newVerificationChallenge draws a code and builds the challenge that stores only its digest.
func (a *Auth) newVerificationChallenge(userID domain.ID, now time.Time) (domain.OTPCode, domain.OTPChallenge, error) {
	id, err := domain.NewID()
	if err != nil {
		return domain.OTPCode{}, domain.OTPChallenge{}, err
	}
	code, err := GenerateOTP()
	if err != nil {
		return domain.OTPCode{}, domain.OTPChallenge{}, err
	}
	challenge, err := domain.NewOTPChallenge(domain.OTPChallenge{
		ID: id, UserID: userID, Purpose: domain.OTPPurposeEmailVerification,
		OTPHash:   a.otp.Digest(domain.OTPPurposeEmailVerification, id, userID, code),
		CreatedAt: now, ExpiresAt: now.Add(otpLifetime),
	})
	if err != nil {
		return domain.OTPCode{}, domain.OTPChallenge{}, err
	}
	return code, challenge, nil
}

func verificationRequested(userID domain.ID) auditSpec {
	return auditSpec{
		Type: domain.EventAuthEmailVerificationRequested, Outcome: domain.AuditOutcomeSuccess,
		TargetType: "user", TargetID: userID.String(),
	}
}

// send hands an email to the mailer after the transaction committed. A failure is logged without
// the address, the code or the error text (which may repeat the address) and never changes the
// answer: the user can ask for a new code after the cooldown.
func (a *Auth) send(meta domain.RequestMeta, kind string, deliver func() error) {
	if err := deliver(); err != nil {
		a.log.Warn("could not hand an email to the mailer",
			slog.String("event", "auth.mail_not_sent"),
			slog.String("request_id", meta.RequestID.String()),
			slog.String("kind", kind))
	}
}
