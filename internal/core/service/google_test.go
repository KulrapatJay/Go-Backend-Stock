package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

type fakeFlows struct {
	port.OIDCFlowRepository
	inserted []domain.OIDCFlow
}

func (f *fakeFlows) Insert(_ context.Context, fl domain.OIDCFlow) error {
	f.inserted = append(f.inserted, fl)
	return nil
}

// googleRepos adds the OIDC flows to the fakes of newTestAuth.
type googleRepos struct {
	*fakeRepositories
	flows *fakeFlows
}

func (r *googleRepos) OIDCFlows() port.OIDCFlowRepository { return r.flows }

type fakeIdentity struct {
	requests []domain.AuthorizationRequest
	urlErr   error
}

func (f *fakeIdentity) AuthorizationURL(_ context.Context, req domain.AuthorizationRequest) (string, error) {
	f.requests = append(f.requests, req)
	if f.urlErr != nil {
		return "", f.urlErr
	}
	return "https://accounts.example/auth?state=" + req.State, nil
}

func (f *fakeIdentity) Exchange(context.Context, domain.CodeExchange) (domain.VerifiedIdentity, error) {
	return domain.VerifiedIdentity{}, errors.New("not used by these tests")
}

type googleTestAuth struct {
	testAuth
	identity *fakeIdentity
	flows    *fakeFlows
	codec    *OIDCCodec
}

func newGoogleTestAuth(t *testing.T) googleTestAuth {
	t.Helper()
	codec, err := NewOIDCCodec(bytes.Repeat([]byte("i"), 32))
	if err != nil {
		t.Fatal(err)
	}
	identity, flows := &fakeIdentity{}, &fakeFlows{}
	ta := newTestAuth(t, func(d *AuthDeps) {
		d.Store = &googleRepos{fakeRepositories: d.Store.(*fakeRepositories), flows: flows}
		d.Identity, d.OIDC = identity, codec
	})
	return googleTestAuth{testAuth: ta, identity: identity, flows: flows, codec: codec}
}

func TestStartGoogleWhenDisabled(t *testing.T) {
	ta := newTestAuth(t, nil)
	if ta.auth.GoogleEnabled() {
		t.Error("GoogleEnabled() = true without an identity provider")
	}
	_, err := ta.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLogin}, testMeta(t))
	_ = requireCode(t, err, domain.CodeFeatureDisabled)
}

func TestStartGoogleStoresOnlyHashesAndDerivesTheChallenge(t *testing.T) {
	g := newGoogleTestAuth(t)
	if !g.auth.GoogleEnabled() {
		t.Fatal("GoogleEnabled() = false with an identity provider")
	}
	r, err := g.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLogin}, testMeta(t))
	if err != nil {
		t.Fatalf("StartGoogle() error = %v", err)
	}
	if len(g.identity.requests) != 1 || len(g.flows.inserted) != 1 {
		t.Fatalf("requests = %d, flows = %d; want one of each", len(g.identity.requests), len(g.flows.inserted))
	}
	req, flow, binding := g.identity.requests[0], g.flows.inserted[0], r.BrowserBinding.Reveal()
	for name, v := range map[string]string{"state": req.State, "nonce": req.Nonce, "binding": binding} {
		if !ValidOIDCValue(v) {
			t.Errorf("%s %q is not a 32-byte random value", name, v)
		}
	}
	if req.CodeChallenge != CodeChallenge(g.codec.Verifier(req.State, binding)) {
		t.Error("the challenge must be the S256 of the verifier derived from state and binding")
	}
	if !bytes.Equal(flow.StateHash, HashOIDCValue(req.State)) || !bytes.Equal(flow.NonceHash, HashOIDCValue(req.Nonce)) ||
		!bytes.Equal(flow.BrowserBindingHash, HashOIDCValue(binding)) {
		t.Error("the flow must store the hashes of state, nonce and binding")
	}
	now := NowForStorage(g.clock)
	if flow.Purpose != domain.OIDCPurposeLogin || !flow.UserID.IsZero() || !flow.CreatedAt.Equal(now) ||
		!flow.ExpiresAt.Equal(now.Add(10*time.Minute)) || !r.ExpiresAt.Equal(flow.ExpiresAt) {
		t.Errorf("flow = %+v, redirect expires %v", flow, r.ExpiresAt)
	}
	if r.URL != "https://accounts.example/auth?state="+req.State {
		t.Errorf("URL = %q, want the provider's", r.URL)
	}
	if len(g.uow.audit.events) != 0 {
		t.Error("starting a flow writes no audit")
	}
}

func TestStartGoogleLinkNeedsAndBindsTheCaller(t *testing.T) {
	g := newGoogleTestAuth(t)
	_, err := g.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLink}, testMeta(t))
	_ = requireCode(t, err, domain.CodeUnauthenticated)

	p := domain.Principal{UserID: mustID(t), SessionID: mustID(t)}
	if _, err := g.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLink, Principal: p}, testMeta(t)); err != nil {
		t.Fatal(err)
	}
	if f := g.flows.inserted[len(g.flows.inserted)-1]; f.Purpose != domain.OIDCPurposeLink || f.UserID != p.UserID {
		t.Errorf("link flow = %+v, want it bound to %s", f, p.UserID)
	}
}

func TestStartGoogleWritesNoFlowWhenTheProviderIsDown(t *testing.T) {
	g := newGoogleTestAuth(t)
	g.identity.urlErr = domain.NewError(domain.CodeDependencyUnavailable, "identity provider is unavailable")
	_, err := g.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLogin}, testMeta(t))
	_ = requireCode(t, err, domain.CodeDependencyUnavailable)
	if len(g.flows.inserted) != 0 {
		t.Error("a flow must not be stored when the provider cannot build the URL")
	}
}

func TestStartGoogleIsRateLimitedBeforeAnyWork(t *testing.T) {
	g := newGoogleTestAuth(t)
	g.repos.limits.decision = domain.RateLimitDecision{RetryAfter: time.Minute, Scope: domain.RateLimitGoogleStartIP}
	_, err := g.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLogin}, testMeta(t))
	_ = requireCode(t, err, domain.CodeRateLimited)
	if len(g.identity.requests) != 0 || len(g.flows.inserted) != 0 {
		t.Error("a refused start must not call the provider or store a flow")
	}
}

func TestNewAuthNeedsTheOIDCCodecWithAProvider(t *testing.T) {
	deps := validAuthDeps(t)
	deps.Identity = &fakeIdentity{}
	if _, err := NewAuth(deps); err == nil {
		t.Error("NewAuth() accepted an identity provider without the OIDC codec")
	}
}
