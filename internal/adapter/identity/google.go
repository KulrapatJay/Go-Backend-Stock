// Package identity holds the external identity providers (Google OpenID Connect in phase 1).
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// GoogleIssuer is Google's OpenID Connect issuer.
const GoogleIssuer = "https://accounts.google.com"

// GoogleConfig configures NewGoogle. It never prints, marshals or logs ClientSecret.
type GoogleConfig struct {
	Issuer       string // GoogleIssuer; a test server in tests
	ClientID     string
	ClientSecret string
	RedirectURL  string        // PUBLIC_ORIGIN + /api/v1/auth/google/callback
	Timeout      time.Duration // the limit of each call to the provider
	HTTPClient   *http.Client  // nil = a client with Timeout
}

func (c GoogleConfig) String() string {
	return "GoogleConfig{issuer=" + c.Issuer + " client_id=" + c.ClientID + " redirect_url=" + c.RedirectURL + "}"
}

// GoString makes %#v print the same safe summary.
func (c GoogleConfig) GoString() string { return c.String() }

// MarshalJSON marshals the same safe summary.
func (c GoogleConfig) MarshalJSON() ([]byte, error) { return json.Marshal(c.String()) }

// LogValue logs the safe fields only.
func (c GoogleConfig) LogValue() slog.Value {
	return slog.GroupValue(slog.String("issuer", c.Issuer), slog.String("client_id", c.ClientID), slog.String("redirect_url", c.RedirectURL))
}

// Google implements port.IdentityProvider with go-oidc and oauth2. It discovers the provider on
// first use and keeps the result; a failed discovery is retried by the next call, so the API starts
// even while Google is unreachable. Safe for concurrent use.
type Google struct {
	cfg    GoogleConfig
	client *http.Client

	mu   sync.Mutex
	disc *discovered
}

// discovered is what one successful discovery gives: the OAuth client, and one ID token verifier
// whose key cache every sign-in shares.
type discovered struct {
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

var _ port.IdentityProvider = (*Google)(nil)

// NewGoogle validates cfg. It makes no network call.
func NewGoogle(cfg GoogleConfig) (*Google, error) {
	switch {
	case cfg.Issuer == "", cfg.ClientID == "", cfg.ClientSecret == "", cfg.RedirectURL == "":
		return nil, errors.New("google identity provider is missing a setting")
	case cfg.Timeout <= 0:
		return nil, errors.New("google identity provider timeout must be positive")
	}
	var client *http.Client
	switch {
	case cfg.HTTPClient == nil:
		client = &http.Client{Timeout: cfg.Timeout}
	case cfg.HTTPClient.Timeout == 0:
		// The key fetch runs on a context that outlives the request, so the client timeout is its
		// only bound. Copy the client so the caller's one is left alone.
		c := *cfg.HTTPClient
		c.Timeout = cfg.Timeout
		client = &c
	default:
		client = cfg.HTTPClient
	}
	return &Google{cfg: cfg, client: client}, nil
}

func (g *Google) String() string { return "identity.Google{" + g.cfg.String() + "}" }

// GoString makes %#v print the same safe summary.
func (g *Google) GoString() string { return g.String() }

// LogValue logs the safe configuration only.
func (g *Google) LogValue() slog.Value { return g.cfg.LogValue() }

// AuthorizationURL implements port.IdentityProvider.
func (g *Google) AuthorizationURL(ctx context.Context, req domain.AuthorizationRequest) (string, error) {
	d, err := g.discover(ctx)
	if err != nil {
		return "", err
	}
	return d.oauth.AuthCodeURL(req.State,
		oauth2.SetAuthURLParam("nonce", req.Nonce),
		oauth2.SetAuthURLParam("code_challenge", req.CodeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	), nil
}

// Exchange implements port.IdentityProvider. go-oidc checks the signature against the provider's
// published keys, the issuer, the audience (the client ID) and the expiry. A provider outage is
// reported as unavailable and never as a refused sign-in; messages carry no response bodies.
func (g *Google) Exchange(ctx context.Context, ex domain.CodeExchange) (domain.VerifiedIdentity, error) {
	d, err := g.discover(ctx)
	if err != nil {
		return domain.VerifiedIdentity{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	ctx = oidc.ClientContext(ctx, g.client)

	tok, err := d.oauth.Exchange(ctx, ex.Code.Reveal(), oauth2.VerifierOption(ex.CodeVerifier.Reveal()))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil {
			status := re.Response.StatusCode
			if tokenStatusIsOutage(status) {
				return domain.VerifiedIdentity{}, unavailable(fmt.Errorf("token endpoint answered %d", status))
			}
			return domain.VerifiedIdentity{}, fmt.Errorf("%w: token endpoint answered %d", domain.ErrIdentityRejected, status)
		}
		return domain.VerifiedIdentity{}, unavailable(err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return domain.VerifiedIdentity{}, fmt.Errorf("%w: no ID token", domain.ErrIdentityRejected)
	}
	var fetch keyFetchFailure
	idt, err := d.verifier.Verify(context.WithValue(ctx, keyFetchKey{}, &fetch), raw)
	if err != nil {
		if fetch.failed() {
			return domain.VerifiedIdentity{}, unavailable(errors.New("could not fetch the provider's signing keys"))
		}
		if isNetwork(err) {
			return domain.VerifiedIdentity{}, unavailable(err)
		}
		return domain.VerifiedIdentity{}, fmt.Errorf("%w: %w", domain.ErrIdentityRejected, err)
	}
	if idt.Subject == "" {
		return domain.VerifiedIdentity{}, fmt.Errorf("%w: no subject", domain.ErrIdentityRejected)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idt.Claims(&claims); err != nil {
		return domain.VerifiedIdentity{}, fmt.Errorf("%w: unreadable claims", domain.ErrIdentityRejected)
	}
	return domain.VerifiedIdentity{
		Issuer: normalizeIssuer(g.cfg.Issuer, idt.Issuer), Subject: idt.Subject,
		Email: claims.Email, EmailVerified: claims.EmailVerified, Nonce: idt.Nonce,
	}, nil
}

// discover returns the cached discovery result, or runs discovery once more.
func (g *Google) discover(ctx context.Context) (*discovered, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.disc != nil {
		return g.disc, nil
	}
	ctx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, g.client), g.cfg.Issuer)
	if err != nil {
		return nil, unavailable(err)
	}
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := p.Claims(&doc); err != nil || doc.JWKSURI == "" {
		return nil, unavailable(errors.New("discovery document has no jwks_uri"))
	}
	// The key set keeps its own context, so the timeout above does not cut later key fetches; the
	// client timeout bounds them.
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), g.client), doc.JWKSURI)
	endpoint := p.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	g.disc = &discovered{
		oauth: &oauth2.Config{
			ClientID: g.cfg.ClientID, ClientSecret: g.cfg.ClientSecret, RedirectURL: g.cfg.RedirectURL,
			Endpoint: endpoint, Scopes: []string{oidc.ScopeOpenID, "email"},
		},
		verifier: oidc.NewVerifier(g.cfg.Issuer, fetchRecorder{keys}, &oidc.Config{
			ClientID: g.cfg.ClientID, SupportedSigningAlgs: []string{oidc.RS256},
		}),
	}
	return g.disc, nil
}

type keyFetchKey struct{}

// keyFetchFailure is where fetchRecorder notes that fetching the provider's keys failed.
// go-oidc flattens the key set's error into text, so the context carries the signal instead.
type keyFetchFailure struct {
	mu  sync.Mutex
	err error
}

func (f *keyFetchFailure) record(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *keyFetchFailure) failed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err != nil
}

// fetchRecorder wraps the remote key set. go-oidc wraps a failed fetch with %w ("fetching keys
// ..."), while a signature that matches no key is a plain error, so Unwrap tells them apart.
type fetchRecorder struct{ inner oidc.KeySet }

func (r fetchRecorder) VerifySignature(ctx context.Context, jwt string) ([]byte, error) {
	payload, err := r.inner.VerifySignature(ctx, jwt)
	if err != nil && errors.Unwrap(err) != nil {
		if f, ok := ctx.Value(keyFetchKey{}).(*keyFetchFailure); ok {
			f.record(err)
		}
	}
	return payload, err
}

// tokenStatusIsOutage separates a provider or credential problem from a user being refused: 401
// means our client credentials are wrong, which must not look like a refused sign-in.
func tokenStatusIsOutage(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

// normalizeIssuer maps Google's allowed "accounts.google.com" issuer variant to GoogleIssuer.
func normalizeIssuer(configured, got string) string {
	if configured == GoogleIssuer && got == "accounts.google.com" {
		return GoogleIssuer
	}
	return got
}

func unavailable(err error) error {
	return fmt.Errorf("%w: %w", domain.NewError(domain.CodeDependencyUnavailable, "identity provider is unavailable"), err)
}

func isNetwork(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &ne)
}
