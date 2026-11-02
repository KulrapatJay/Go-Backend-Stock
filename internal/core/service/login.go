package service

import (
	"context"
	"errors"
	"log/slog"
	"unicode/utf8"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// maxLoginPasswordRunes is the longest password registration accepts. Login refuses a longer one
// before hashing, so a huge body cannot buy an expensive Argon2 run (milestone 4 design section 4).
const maxLoginPasswordRunes = 32

// errPasswordChanged means the hash changed between the check and the transaction (a password
// reset ran in between); the login fails like a wrong password.
var errPasswordChanged = errors.New("password changed during login")

// Login implements port.AuthService (milestone 4 design section 5).
func (a *Auth) Login(ctx context.Context, in domain.LoginInput, meta domain.RequestMeta) (domain.SessionGrant, error) {
	rules, err := a.limits.Login(in.Email, meta.ClientIP)
	if err != nil {
		return domain.SessionGrant{}, err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return domain.SessionGrant{}, err
	}
	password := in.Password.Reveal()
	if password == "" || utf8.RuneCountInString(password) > maxLoginPasswordRunes {
		return domain.SessionGrant{}, a.loginFailed(ctx, meta, "invalid_credentials", domain.ErrInvalidCredentials)
	}

	creds, err := a.store.Users().CredentialsByEmail(ctx, in.Email)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && !creds.HasPassword()) {
		// Spend the same hash as a real check, so the time does not tell the account apart.
		if err := a.hasher.VerifyDummy(ctx, password); err != nil {
			return domain.SessionGrant{}, err
		}
		return domain.SessionGrant{}, a.loginFailed(ctx, meta, "invalid_credentials", domain.ErrInvalidCredentials)
	}
	if err != nil {
		return domain.SessionGrant{}, err
	}
	ok, err := a.hasher.Verify(ctx, creds.PasswordHash, password)
	if err != nil {
		return domain.SessionGrant{}, err
	}
	if !ok {
		return domain.SessionGrant{}, a.loginFailed(ctx, meta, "invalid_credentials", domain.ErrInvalidCredentials)
	}
	if creds.User.EmailVerifiedAt == nil {
		return domain.SessionGrant{}, a.loginFailed(ctx, meta, "email_not_verified", domain.ErrEmailNotVerified)
	}

	upgraded := a.upgradeHash(ctx, meta, creds.PasswordHash, in.Password)
	now := NowForStorage(a.clock)
	session, token, err := a.issueSession(creds.User.ID, now)
	if err != nil {
		return domain.SessionGrant{}, err
	}

	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		if _, err := tx.Users().Lock(ctx, creds.User.ID); err != nil {
			return err
		}
		current, err := tx.Users().CredentialsByEmail(ctx, in.Email)
		if errors.Is(err, domain.ErrNotFound) {
			return errPasswordChanged
		}
		if err != nil {
			return err
		}
		if current.User.ID != creds.User.ID || current.PasswordHash != creds.PasswordHash {
			return errPasswordChanged
		}
		if upgraded != "" {
			if err := tx.Users().SetPassword(ctx, creds.User.ID, upgraded, now); err != nil {
				return err
			}
		}
		if err := tx.Sessions().Insert(ctx, session); err != nil {
			return err
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthLoginSucceeded, Outcome: domain.AuditOutcomeSuccess, Actor: creds.User.ID,
			SessionID: session.ID, TargetType: "user", TargetID: creds.User.ID.String(), Method: "password",
		})
	})
	if errors.Is(err, errPasswordChanged) {
		return domain.SessionGrant{}, a.loginFailed(ctx, meta, "invalid_credentials", domain.ErrInvalidCredentials)
	}
	if err != nil {
		return domain.SessionGrant{}, err
	}
	return domain.SessionGrant{User: creds.User, Token: token, ExpiresAt: session.ExpiresAt}, nil
}

// upgradeHash returns a new hash when the stored one uses old parameters, or "" to keep it. A busy
// hasher skips the upgrade: the next login tries again, and this one still succeeds.
func (a *Auth) upgradeHash(ctx context.Context, meta domain.RequestMeta, stored domain.PasswordHash, password domain.SecretText) domain.PasswordHash {
	need, err := a.hasher.NeedsRehash(stored)
	if err != nil || !need {
		return ""
	}
	fresh, err := a.hasher.Rehash(ctx, password)
	if err != nil {
		a.log.Warn("could not upgrade a password hash at login",
			slog.String("event", "auth.rehash_skipped"),
			slog.String("request_id", meta.RequestID.String()))
		return ""
	}
	return fresh
}

// loginFailed audits a failed login in its own transaction and returns result, or the audit error
// when the event could not be stored (milestone 4 design section 5: log it and answer 500).
func (a *Auth) loginFailed(ctx context.Context, meta domain.RequestMeta, reason string, result error) error {
	if err := a.recordFailure(ctx, meta, auditSpec{
		Type: domain.EventAuthLoginFailed, Outcome: domain.AuditOutcomeFailure,
		TargetType: "login", Reason: reason, Method: "password",
	}); err != nil {
		return err
	}
	return result
}

// CurrentUser implements port.AuthService.
func (a *Auth) CurrentUser(ctx context.Context, p domain.Principal) (domain.User, error) {
	if p.IsZero() {
		return domain.User{}, domain.ErrUnauthenticated
	}
	u, err := a.store.Users().ByID(ctx, p.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, domain.ErrUnauthenticated
	}
	return u, err
}

// Logout implements port.AuthService. Revoke reports whether this call revoked the session, so a
// repeated or concurrent logout writes one auth.logout only.
func (a *Auth) Logout(ctx context.Context, p domain.Principal, meta domain.RequestMeta) error {
	if p.IsZero() {
		return nil
	}
	now := NowForStorage(a.clock)
	return a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		revoked, err := tx.Sessions().Revoke(ctx, p.SessionID, now, domain.RevokeReasonLogout)
		if err != nil || !revoked {
			return err
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthLogout, Outcome: domain.AuditOutcomeSuccess, Actor: p.UserID,
			SessionID: p.SessionID, TargetType: "session", TargetID: p.SessionID.String(),
		})
	})
}
