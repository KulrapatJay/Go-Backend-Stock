package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

// fakeIdentityProvider stands in for Google at the port. Exchange accepts only a verifier whose
// S256 matches the challenge of a request it saw, and answers with that request's nonce unless a
// test forces another.
type fakeIdentityProvider struct {
	mu          sync.Mutex
	requests    []domain.AuthorizationRequest
	exchanged   []domain.CodeExchange
	identity    domain.VerifiedIdentity
	urlErr      error
	exchangeErr error
	nonce       string // forced nonce; "" = the nonce of the matching request
}

func newFakeIdentityProvider() *fakeIdentityProvider { return &fakeIdentityProvider{} }

func (f *fakeIdentityProvider) AuthorizationURL(_ context.Context, req domain.AuthorizationRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req) // recorded even on error, so a test can look up the state
	if f.urlErr != nil {
		return "", f.urlErr
	}
	q := url.Values{"state": {req.State}, "nonce": {req.Nonce}, "code_challenge": {req.CodeChallenge}, "code_challenge_method": {"S256"}}
	return "https://accounts.example.test/auth?" + q.Encode(), nil
}

func (f *fakeIdentityProvider) Exchange(_ context.Context, ex domain.CodeExchange) (domain.VerifiedIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchanged = append(f.exchanged, ex)
	if f.exchangeErr != nil {
		return domain.VerifiedIdentity{}, f.exchangeErr
	}
	challenge := service.CodeChallenge(ex.CodeVerifier.Reveal())
	for _, r := range f.requests {
		if r.CodeChallenge == challenge {
			id := f.identity
			id.Nonce = r.Nonce
			if f.nonce != "" {
				id.Nonce = f.nonce
			}
			return id, nil
		}
	}
	return domain.VerifiedIdentity{}, domain.ErrIdentityRejected
}

func (f *fakeIdentityProvider) setIdentity(subject, email string, verified bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identity = domain.VerifiedIdentity{Issuer: "https://accounts.example.test", Subject: subject, Email: email, EmailVerified: verified}
}

func (f *fakeIdentityProvider) setURLError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urlErr = err
}

func (f *fakeIdentityProvider) setExchangeError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchangeErr = err
}

func (f *fakeIdentityProvider) forceNonce(nonce string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nonce = nonce
}

func (f *fakeIdentityProvider) lastRequest(t *testing.T) domain.AuthorizationRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("the identity provider received no authorization request")
	}
	return f.requests[len(f.requests)-1]
}

func (f *fakeIdentityProvider) exchanges() []domain.CodeExchange {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.CodeExchange(nil), f.exchanged...)
}

func (b *browser) startGoogle() (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/google/start", nil, nil)
}

func (b *browser) linkGoogle() (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/google/link", nil, nil)
}

func (b *browser) googleCallback(q url.Values) (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, "/api/v1/auth/google/callback?"+q.Encode(), nil, nil)
}

func decodeStart(t *testing.T, body []byte) GoogleStartResponse {
	t.Helper()
	var out GoogleStartResponse
	if err := json.Unmarshal(body, &out); err != nil || out.AuthorizationURL == "" {
		t.Fatalf("start body %s: %v", body, err)
	}
	return out
}

type flowRow struct {
	Purpose  string
	UserID   string // "" when NULL
	Lifetime time.Duration
	Consumed bool
}

func (h *authHarness) flowByState(t *testing.T, state string) (flowRow, bool) {
	t.Helper()
	var r flowRow
	var created, expires time.Time
	err := h.admin.QueryRow(context.Background(),
		`SELECT purpose, COALESCE(user_id::text, ''), created_at, expires_at, consumed_at IS NOT NULL
		 FROM app.oauth_flows WHERE state_hash = $1`, service.HashOIDCValue(state),
	).Scan(&r.Purpose, &r.UserID, &created, &expires, &r.Consumed)
	if err != nil {
		return flowRow{}, false
	}
	r.Lifetime = expires.Sub(created)
	return r, true
}
