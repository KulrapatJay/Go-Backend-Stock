package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// GoogleEnabled implements port.AuthService.
func (a *Auth) GoogleEnabled() bool { return a.identity != nil }

// StartGoogle implements port.AuthService (milestone 4 design section 6). It builds the provider URL
// before it stores the flow, so an unreachable provider leaves no row behind. Only the hashes of the
// state, the nonce and the browser binding are stored; the PKCE verifier is derived again at the
// callback. Starting a flow writes no audit event.
func (a *Auth) StartGoogle(ctx context.Context, in domain.OIDCStart, meta domain.RequestMeta) (domain.OIDCRedirect, error) {
	if a.identity == nil {
		return domain.OIDCRedirect{}, domain.ErrFeatureDisabled
	}
	var flowUser domain.ID
	switch in.Purpose {
	case domain.OIDCPurposeLogin:
	case domain.OIDCPurposeLink:
		if in.Principal.IsZero() {
			return domain.OIDCRedirect{}, domain.ErrUnauthenticated
		}
		flowUser = in.Principal.UserID
	default:
		return domain.OIDCRedirect{}, domain.NewError(domain.CodeInternal, "unknown Google flow purpose")
	}

	rules, err := a.limits.GoogleStart(meta.ClientIP)
	if err != nil {
		return domain.OIDCRedirect{}, err
	}
	if err := a.admit(ctx, rules, meta, flowUser); err != nil {
		return domain.OIDCRedirect{}, err
	}

	var values [3]string // state, nonce, browser binding
	for i := range values {
		if values[i], err = NewOIDCValue(); err != nil {
			return domain.OIDCRedirect{}, err
		}
	}
	state, nonce, binding := values[0], values[1], values[2]
	url, err := a.identity.AuthorizationURL(ctx, domain.AuthorizationRequest{
		State: state, Nonce: nonce, CodeChallenge: CodeChallenge(a.oidc.Verifier(state, binding)),
	})
	if err != nil {
		return domain.OIDCRedirect{}, err
	}

	id, err := domain.NewID()
	if err != nil {
		return domain.OIDCRedirect{}, err
	}
	now := NowForStorage(a.clock)
	flow, err := domain.NewOIDCFlow(domain.OIDCFlow{
		ID: id, Purpose: in.Purpose, UserID: flowUser,
		StateHash: HashOIDCValue(state), NonceHash: HashOIDCValue(nonce), BrowserBindingHash: HashOIDCValue(binding),
		CreatedAt: now, ExpiresAt: now.Add(oidcFlowLifetime),
	})
	if err != nil {
		return domain.OIDCRedirect{}, err
	}
	if err := a.store.OIDCFlows().Insert(ctx, flow); err != nil {
		return domain.OIDCRedirect{}, err
	}
	return domain.OIDCRedirect{URL: url, BrowserBinding: domain.NewSecretText(binding), ExpiresAt: flow.ExpiresAt}, nil
}

// CompleteGoogle implements port.AuthService (milestone 4 design section 6). The flow is consumed
// before the code is exchanged, so a replayed callback never reaches the provider twice, and the
// exchange runs outside any transaction.
func (a *Auth) CompleteGoogle(ctx context.Context, in domain.OIDCCallback, meta domain.RequestMeta) (domain.OIDCResult, error) {
	if a.identity == nil {
		return domain.OIDCResult{}, domain.ErrFeatureDisabled
	}
	binding := in.BrowserBinding.Reveal()
	if !ValidOIDCValue(in.State) || !ValidOIDCValue(binding) {
		return a.flowNotFound(meta), nil
	}
	flow, err := a.store.OIDCFlows().Consume(ctx, HashOIDCValue(in.State), HashOIDCValue(binding), NowForStorage(a.clock))
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return a.flowNotFound(meta), nil
	case err != nil:
		return domain.OIDCResult{}, err
	}

	if in.ProviderError != "" || in.Code.IsZero() {
		return a.googleFailed(ctx, meta, flow, in.Principal, "provider_error")
	}
	if flow.Purpose == domain.OIDCPurposeLink && (in.Principal.IsZero() || in.Principal.UserID != flow.UserID) {
		return a.googleFailed(ctx, meta, flow, in.Principal, "session_mismatch")
	}
	id, err := a.identity.Exchange(ctx, domain.CodeExchange{
		Code: in.Code, CodeVerifier: domain.NewSecretText(a.oidc.Verifier(in.State, binding)),
	})
	switch {
	case errors.Is(err, domain.ErrIdentityRejected):
		return a.googleFailed(ctx, meta, flow, in.Principal, "exchange_rejected")
	case err != nil:
		return domain.OIDCResult{}, err
	}
	if subtle.ConstantTimeCompare(HashOIDCValue(id.Nonce), flow.NonceHash) != 1 {
		return a.googleFailed(ctx, meta, flow, in.Principal, "nonce_mismatch")
	}
	if flow.Purpose == domain.OIDCPurposeLink {
		return a.linkGoogle(ctx, meta, flow, in.Principal, id)
	}
	return a.loginGoogle(ctx, meta, flow, id)
}

// loginGoogle signs in the account linked to the Google subject, or creates one for a new, verified
// email. An email that already has an account is never linked automatically (AGENTS.md).
func (a *Auth) loginGoogle(ctx context.Context, meta domain.RequestMeta, flow domain.OIDCFlow, id domain.VerifiedIdentity) (domain.OIDCResult, error) {
	link, err := a.store.Identities().BySubject(ctx, domain.IdentityProviderGoogle, id.Subject)
	switch {
	case err == nil:
		return a.openGoogleSession(ctx, meta, link.UserID)
	case !errors.Is(err, domain.ErrNotFound):
		return domain.OIDCResult{}, err
	}
	if !id.EmailVerified {
		return a.googleFailed(ctx, meta, flow, domain.Principal{}, "email_not_verified")
	}
	email, err := domain.ParseEmail(id.Email)
	if err != nil {
		return a.googleFailed(ctx, meta, flow, domain.Principal{}, "email_invalid")
	}
	return a.registerGoogle(ctx, meta, flow, id, email)
}

// registerGoogle creates a verified account without a password, its Google identity and a session in
// one transaction. If the email is taken it checks the subject again inside the transaction: a
// second tab may have just created this same account, and then this tab signs in to it.
func (a *Auth) registerGoogle(ctx context.Context, meta domain.RequestMeta, flow domain.OIDCFlow, id domain.VerifiedIdentity, email domain.Email) (domain.OIDCResult, error) {
	now := NowForStorage(a.clock)
	userID, err := domain.NewID()
	if err != nil {
		return domain.OIDCResult{}, err
	}
	linkID, err := domain.NewID()
	if err != nil {
		return domain.OIDCResult{}, err
	}
	link, err := domain.NewAuthProvider(domain.AuthProvider{
		ID: linkID, UserID: userID, Provider: domain.IdentityProviderGoogle, ProviderSubject: id.Subject, CreatedAt: now,
	})
	if err != nil {
		return a.googleFailed(ctx, meta, flow, domain.Principal{}, "token_invalid")
	}
	session, token, err := a.issueSession(userID, now)
	if err != nil {
		return domain.OIDCResult{}, err
	}
	user := domain.User{ID: userID, Email: email, EmailVerifiedAt: &now, CreatedAt: now, UpdatedAt: now}

	var linkRequired bool
	var existing domain.ID
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		linkRequired, existing = false, domain.ID{}
		if err := tx.Users().Insert(ctx, user); err != nil {
			if !errors.Is(err, domain.ErrIdentityConflict) {
				return err
			}
			other, err := tx.Identities().BySubject(ctx, domain.IdentityProviderGoogle, id.Subject)
			if err == nil {
				existing = other.UserID
				return nil
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			linkRequired = true
			return a.appendAudit(ctx, tx, meta, googleFailure(flow, domain.Principal{}, "link_required"))
		}
		if err := tx.Identities().Insert(ctx, link); err != nil {
			return err
		}
		if err := tx.Sessions().Insert(ctx, session); err != nil {
			return err
		}
		if err := a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthRegistered, Outcome: domain.AuditOutcomeSuccess, Actor: userID,
			TargetType: "user", TargetID: userID.String(), Method: "google",
		}); err != nil {
			return err
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthLoginSucceeded, Outcome: domain.AuditOutcomeSuccess, Actor: userID,
			SessionID: session.ID, TargetType: "user", TargetID: userID.String(), Method: "google",
		})
	})
	switch {
	case err != nil:
		return domain.OIDCResult{}, err
	case linkRequired:
		return domain.OIDCResult{Outcome: domain.OIDCOutcomeLinkRequired}, nil
	case !existing.IsZero():
		return a.openGoogleSession(ctx, meta, existing)
	}
	return domain.OIDCResult{Outcome: domain.OIDCOutcomeSuccess, Session: domain.SessionGrant{User: user, Token: token, ExpiresAt: session.ExpiresAt}}, nil
}

// openGoogleSession signs userID in: lock the user, insert the session and audit it together.
func (a *Auth) openGoogleSession(ctx context.Context, meta domain.RequestMeta, userID domain.ID) (domain.OIDCResult, error) {
	now := NowForStorage(a.clock)
	session, token, err := a.issueSession(userID, now)
	if err != nil {
		return domain.OIDCResult{}, err
	}
	var user domain.User
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		u, err := tx.Users().Lock(ctx, userID)
		if err != nil {
			return err
		}
		user = u
		if err := tx.Sessions().Insert(ctx, session); err != nil {
			return err
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthLoginSucceeded, Outcome: domain.AuditOutcomeSuccess, Actor: userID,
			SessionID: session.ID, TargetType: "user", TargetID: userID.String(), Method: "google",
		})
	})
	if err != nil {
		return domain.OIDCResult{}, err
	}
	return domain.OIDCResult{Outcome: domain.OIDCOutcomeSuccess, Session: domain.SessionGrant{User: user, Token: token, ExpiresAt: session.ExpiresAt}}, nil
}

// linkGoogle adds the Google identity to the signed-in account that started the flow. It opens no
// session. A subject linked elsewhere, or a second Google account for this user, is refused and
// audited in the same transaction.
func (a *Auth) linkGoogle(ctx context.Context, meta domain.RequestMeta, flow domain.OIDCFlow, p domain.Principal, id domain.VerifiedIdentity) (domain.OIDCResult, error) {
	linkID, err := domain.NewID()
	if err != nil {
		return domain.OIDCResult{}, err
	}
	link, err := domain.NewAuthProvider(domain.AuthProvider{
		ID: linkID, UserID: p.UserID, Provider: domain.IdentityProviderGoogle, ProviderSubject: id.Subject, CreatedAt: NowForStorage(a.clock),
	})
	if err != nil {
		return a.googleFailed(ctx, meta, flow, p, "token_invalid")
	}
	var conflict bool
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		conflict = false
		if _, err := tx.Users().Lock(ctx, p.UserID); err != nil {
			return err
		}
		if err := tx.Identities().Insert(ctx, link); err != nil {
			if !errors.Is(err, domain.ErrIdentityConflict) {
				return err
			}
			conflict = true
			return a.appendAudit(ctx, tx, meta, googleFailure(flow, p, "identity_conflict"))
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthIdentityLinked, Outcome: domain.AuditOutcomeSuccess, Actor: p.UserID,
			SessionID: p.SessionID, TargetType: "auth_provider", TargetID: link.ID.String(), Provider: "google",
		})
	})
	switch {
	case err != nil:
		return domain.OIDCResult{}, err
	case conflict:
		return domain.OIDCResult{Outcome: domain.OIDCOutcomeFailed}, nil
	}
	return domain.OIDCResult{Outcome: domain.OIDCOutcomeLinked}, nil
}

// googleFailed audits a refused callback in its own transaction and answers "failed".
func (a *Auth) googleFailed(ctx context.Context, meta domain.RequestMeta, flow domain.OIDCFlow, p domain.Principal, reason string) (domain.OIDCResult, error) {
	if err := a.recordFailure(ctx, meta, googleFailure(flow, p, reason)); err != nil {
		return domain.OIDCResult{}, err
	}
	return domain.OIDCResult{Outcome: domain.OIDCOutcomeFailed}, nil
}

// googleFailure is the audit event of a refused callback. A login flow has no proven caller. A link
// flow audits identity_link_failed, with the caller as actor only when its session is the one that
// started the flow.
func googleFailure(flow domain.OIDCFlow, p domain.Principal, reason string) auditSpec {
	if flow.Purpose != domain.OIDCPurposeLink {
		return auditSpec{
			Type: domain.EventAuthLoginFailed, Outcome: domain.AuditOutcomeFailure,
			TargetType: "oidc_flow", TargetID: flow.ID.String(), Reason: reason, Method: "google",
		}
	}
	s := auditSpec{
		Type: domain.EventAuthIdentityLinkFailed, Outcome: domain.AuditOutcomeFailure,
		TargetType: "oidc_flow", TargetID: flow.ID.String(), Reason: reason, Provider: "google",
	}
	if !p.IsZero() && p.UserID == flow.UserID {
		s.Actor, s.SessionID = p.UserID, p.SessionID
	}
	return s
}

// flowNotFound answers a callback that matches no open flow. It writes no audit: nothing proves the
// request belongs to a flow, and an unauthenticated GET must not fill audit_events.
func (a *Auth) flowNotFound(meta domain.RequestMeta) domain.OIDCResult {
	a.log.Info("google callback matched no open flow",
		slog.String("event", "auth.oidc_flow_not_found"),
		slog.String("request_id", meta.RequestID.String()))
	return domain.OIDCResult{Outcome: domain.OIDCOutcomeFailed}
}
