package service

import (
	"context"

	"go-backend-stock/internal/core/domain"
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
