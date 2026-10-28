package http

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/adapter/identity"
	"go-backend-stock/internal/adapter/identity/identitytest"
	"go-backend-stock/internal/core/domain"
)

// newRealGoogleHarness runs the real adapter against the fake provider, so the whole path is the
// production one: HTTP → service → go-oidc → token endpoint → signed ID token.
func newRealGoogleHarness(t *testing.T) (*authHarness, *identitytest.Provider) {
	t.Helper()
	p := identitytest.New(t)
	g, err := identity.NewGoogle(identity.GoogleConfig{
		Issuer: p.Issuer(), ClientID: identitytest.ClientID, ClientSecret: identitytest.ClientSecret,
		RedirectURL: testOrigin + "/api/v1/auth/google/callback", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return newAuthHarness(t, withIdentityProvider(g)), p
}

// startAtFakeGoogle starts a flow and plays the Google page: it reads the flow values from the
// authorization URL and registers a code for grant.
func startAtFakeGoogle(t *testing.T, b *browser, p *identitytest.Provider, grant identitytest.Grant) url.Values {
	t.Helper()
	resp, body := b.startGoogle()
	out := decodeStart(t, body)
	if resp.StatusCode != 200 || !strings.HasPrefix(out.AuthorizationURL, p.Issuer()+"/auth?") {
		t.Fatalf("start = %d, url %s; want the fake provider's authorization endpoint", resp.StatusCode, out.AuthorizationURL)
	}
	state, nonce, challenge := identitytest.ParamsFromURL(t, out.AuthorizationURL)
	if grant.Nonce == "" {
		grant.Nonce = nonce
	}
	if grant.CodeChallenge == "" {
		grant.CodeChallenge = challenge
	}
	return url.Values{"code": {p.Authorize(t, grant)}, "state": {state}}
}

func TestGoogleSignInEndToEnd(t *testing.T) {
	h, p := newRealGoogleHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	q := startAtFakeGoogle(t, b, p, identitytest.Grant{Subject: uniqueSubject(t), Email: email, EmailVerified: true})
	resp, body := b.googleCallback(q)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeSuccess)
	if resp, body := b.me(); resp.StatusCode != 200 || decodeUser(t, body).Email != email {
		t.Errorf("me = %d %s", resp.StatusCode, body)
	}
}

func TestGoogleSignInEndToEndRefusesBadTokens(t *testing.T) {
	h, p := newRealGoogleHarness(t)
	for name, grant := range map[string]identitytest.Grant{
		"forged signature": {SignWith: identitytest.NewKey(t)},
		"other audience":   {Audience: "someone-else.apps.example"},
		"expired":          {Expiry: -10 * time.Minute},
		"wrong verifier":   {CodeChallenge: "a-challenge-of-another-verifier"},
		"other nonce":      {Nonce: "a-nonce-of-another-flow"},
	} {
		t.Run(name, func(t *testing.T) {
			b := h.newBrowser(t)
			email := uniqueEmail(t)
			grant.Subject, grant.Email, grant.EmailVerified = uniqueSubject(t), email, true
			resp, body := b.googleCallback(startAtFakeGoogle(t, b, p, grant))
			requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
			if _, ok := h.user(t, email); ok || b.jar["session"] != "" {
				t.Error("a refused token must not create an account or a session")
			}
		})
	}
}
