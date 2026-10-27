package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/adapter/identity/identitytest"
	"go-backend-stock/internal/core/domain"
)

const testRedirect = "http://localhost:8080/api/v1/auth/google/callback"

func newTestGoogle(t *testing.T, p *identitytest.Provider) *Google {
	t.Helper()
	g, err := NewGoogle(GoogleConfig{
		Issuer: p.Issuer(), ClientID: identitytest.ClientID, ClientSecret: identitytest.ClientSecret,
		RedirectURL: testRedirect, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewGoogle() error = %v", err)
	}
	return g
}

func requireCode(t *testing.T, err error, want domain.ErrorCode) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != want {
		t.Fatalf("error = %v, want code %s", err, want)
	}
}

const testVerifier = "vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv" // 43 characters

func exchange(code string) domain.CodeExchange {
	return domain.CodeExchange{Code: domain.NewSecretText(code), CodeVerifier: domain.NewSecretText(testVerifier)}
}

func TestAuthorizationURLCarriesTheFlowValues(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	raw, err := g.AuthorizationURL(t.Context(), domain.AuthorizationRequest{State: "state-1", Nonce: "nonce-1", CodeChallenge: "challenge-1"})
	if err != nil {
		t.Fatalf("AuthorizationURL() error = %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != p.Issuer()+"/auth" {
		t.Errorf("endpoint = %s, want the discovered authorization endpoint", got)
	}
	want := map[string]string{
		"response_type": "code", "client_id": identitytest.ClientID, "redirect_uri": testRedirect,
		"scope": "openid email", "state": "state-1", "nonce": "nonce-1",
		"code_challenge": "challenge-1", "code_challenge_method": "S256",
	}
	q := u.Query()
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Has("client_secret") || strings.Contains(raw, "ZQXJ") {
		t.Error("the authorization URL must not carry the client secret")
	}
}

func TestExchangeReturnsTheVerifiedIdentity(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	code := p.Authorize(t, identitytest.Grant{
		Subject: "1029384756", Email: "user@example.com", EmailVerified: true, Nonce: "nonce-1",
		CodeChallenge: identitytest.Challenge(testVerifier),
	})
	id, err := g.Exchange(t.Context(), exchange(code))
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if id.Issuer != p.Issuer() || id.Subject != "1029384756" || id.Email != "user@example.com" || !id.EmailVerified || id.Nonce != "nonce-1" {
		t.Errorf("identity = %+v", id)
	}
}

func TestExchangeKeepsAnUnverifiedEmailUnverified(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	code := p.Authorize(t, identitytest.Grant{Subject: "s", Email: "user@example.com", Nonce: "n", CodeChallenge: identitytest.Challenge(testVerifier)})
	id, err := g.Exchange(t.Context(), exchange(code))
	if err != nil || id.EmailVerified {
		t.Errorf("identity = %+v, %v; want email_verified false", id, err)
	}
}

func TestExchangeRejectsABadIDToken(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	foreign := identitytest.NewKey(t)
	for name, grant := range map[string]identitytest.Grant{
		"other audience":    {Audience: "someone-else.apps.example"},
		"other issuer":      {Issuer: "https://accounts.evil.example"},
		"expired":           {Expiry: -10 * time.Minute},
		"foreign signature": {SignWith: foreign},
	} {
		t.Run(name, func(t *testing.T) {
			grant.Subject, grant.Email, grant.Nonce = "s", "user@example.com", "n"
			grant.CodeChallenge = identitytest.Challenge(testVerifier)
			_, err := g.Exchange(t.Context(), exchange(p.Authorize(t, grant)))
			if !errors.Is(err, domain.ErrIdentityRejected) {
				t.Errorf("Exchange() error = %v, want ErrIdentityRejected", err)
			}
		})
	}
}

func TestExchangeRejectsAWrongVerifierAndAReusedCode(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	code := p.Authorize(t, identitytest.Grant{Subject: "s", Nonce: "n", CodeChallenge: identitytest.Challenge(testVerifier)})
	wrong := domain.CodeExchange{Code: domain.NewSecretText(code), CodeVerifier: domain.NewSecretText(strings.Repeat("w", 43))}
	if _, err := g.Exchange(t.Context(), wrong); !errors.Is(err, domain.ErrIdentityRejected) {
		t.Errorf("wrong verifier = %v, want ErrIdentityRejected", err)
	}
	code = p.Authorize(t, identitytest.Grant{Subject: "s", Nonce: "n", CodeChallenge: identitytest.Challenge(testVerifier)})
	if _, err := g.Exchange(t.Context(), exchange(code)); err != nil {
		t.Fatalf("first use = %v", err)
	}
	if _, err := g.Exchange(t.Context(), exchange(code)); !errors.Is(err, domain.ErrIdentityRejected) {
		t.Errorf("reused code = %v, want ErrIdentityRejected", err)
	}
}

func TestDiscoveryFailureIsUnavailableAndRetried(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	req := domain.AuthorizationRequest{State: "s", Nonce: "n", CodeChallenge: "c"}
	p.SetDown(true)
	_, err := g.AuthorizationURL(t.Context(), req)
	requireCode(t, err, domain.CodeDependencyUnavailable)
	p.SetDown(false)
	if _, err := g.AuthorizationURL(t.Context(), req); err != nil {
		t.Errorf("after the provider came back = %v, want discovery to be retried", err)
	}
}

func TestExchangeWhenTheProviderIsDownIsUnavailable(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	if _, err := g.AuthorizationURL(t.Context(), domain.AuthorizationRequest{State: "s", Nonce: "n", CodeChallenge: "c"}); err != nil {
		t.Fatal(err) // discovery succeeded and is cached
	}
	code := p.Authorize(t, identitytest.Grant{Subject: "s", Nonce: "n", CodeChallenge: identitytest.Challenge(testVerifier)})
	p.SetDown(true)
	_, err := g.Exchange(t.Context(), exchange(code))
	requireCode(t, err, domain.CodeDependencyUnavailable)
	if errors.Is(err, domain.ErrIdentityRejected) {
		t.Error("an outage must not look like a refused sign-in")
	}
}

func TestNewGoogleValidatesItsConfig(t *testing.T) {
	valid := GoogleConfig{Issuer: GoogleIssuer, ClientID: "id", ClientSecret: "secret", RedirectURL: testRedirect, Timeout: time.Second}
	for name, mutate := range map[string]func(*GoogleConfig){
		"no issuer":    func(c *GoogleConfig) { c.Issuer = "" },
		"no client id": func(c *GoogleConfig) { c.ClientID = "" },
		"no secret":    func(c *GoogleConfig) { c.ClientSecret = "" },
		"no redirect":  func(c *GoogleConfig) { c.RedirectURL = "" },
		"no timeout":   func(c *GoogleConfig) { c.Timeout = 0 },
	} {
		cfg := valid
		mutate(&cfg)
		if _, err := NewGoogle(cfg); err == nil {
			t.Errorf("%s: NewGoogle() accepted the config", name)
		}
	}
}

func TestGoogleNeverPrintsTheClientSecret(t *testing.T) {
	cfg := GoogleConfig{Issuer: GoogleIssuer, ClientID: "id", ClientSecret: "GOCSPX-ZQXJ", RedirectURL: testRedirect, Timeout: time.Second}
	g, err := NewGoogle(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("google", slog.Any("config", cfg), slog.Any("provider", g))
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"%v": fmt.Sprintf("%v", cfg), "%+v": fmt.Sprintf("%+v", cfg), "%#v": fmt.Sprintf("%#v", cfg),
		"provider %v": fmt.Sprintf("%v", g), "provider %+v": fmt.Sprintf("%+v", g),
		"json": string(data), "slog": logs.String(),
	} {
		if strings.Contains(out, "ZQXJ") {
			t.Errorf("%s leaks the client secret: %s", name, out)
		}
	}
}
