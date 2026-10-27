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

	mu       sync.Mutex
	provider *oidc.Provider
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
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
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
	p, err := g.discover(ctx)
	if err != nil {
		return "", err
	}
	return g.oauth(p).AuthCodeURL(req.State,
		oauth2.SetAuthURLParam("nonce", req.Nonce),
		oauth2.SetAuthURLParam("code_challenge", req.CodeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	), nil
}

// Exchange implements port.IdentityProvider. go-oidc checks the signature against the provider's
// published keys, the issuer, the audience (the client ID) and the expiry.
func (g *Google) Exchange(ctx context.Context, ex domain.CodeExchange) (domain.VerifiedIdentity, error) {
	p, err := g.discover(ctx)
	if err != nil {
		return domain.VerifiedIdentity{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	ctx = oidc.ClientContext(ctx, g.client)

	tok, err := g.oauth(p).Exchange(ctx, ex.Code.Reveal(), oauth2.VerifierOption(ex.CodeVerifier.Reveal()))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode < http.StatusInternalServerError {
			return domain.VerifiedIdentity{}, fmt.Errorf("%w: token endpoint answered %d", domain.ErrIdentityRejected, re.Response.StatusCode)
		}
		return domain.VerifiedIdentity{}, unavailable(err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return domain.VerifiedIdentity{}, fmt.Errorf("%w: no ID token", domain.ErrIdentityRejected)
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: g.cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		if isNetwork(err) {
			return domain.VerifiedIdentity{}, unavailable(err)
		}
		return domain.VerifiedIdentity{}, fmt.Errorf("%w: %w", domain.ErrIdentityRejected, err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idt.Claims(&claims); err != nil {
		return domain.VerifiedIdentity{}, fmt.Errorf("%w: unreadable claims", domain.ErrIdentityRejected)
	}
	return domain.VerifiedIdentity{
		Issuer: idt.Issuer, Subject: idt.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified, Nonce: idt.Nonce,
	}, nil
}

// discover returns the cached provider, or runs discovery once more.
func (g *Google) discover(ctx context.Context) (*oidc.Provider, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider != nil {
		return g.provider, nil
	}
	ctx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, g.client), g.cfg.Issuer)
	if err != nil {
		return nil, unavailable(err)
	}
	g.provider = p
	return p, nil
}

func (g *Google) oauth(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID: g.cfg.ClientID, ClientSecret: g.cfg.ClientSecret, RedirectURL: g.cfg.RedirectURL,
		Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email"},
	}
}

func unavailable(err error) error {
	return fmt.Errorf("%w: %w", domain.NewError(domain.CodeDependencyUnavailable, "identity provider is unavailable"), err)
}

func isNetwork(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &ne)
}
