// Package identitytest is a fake OpenID Connect provider for tests: discovery, a JWKS with an RSA
// key made at start-up, and a token endpoint that checks the client credentials, the code and the
// PKCE verifier before it signs an ID token. Only tests import it.
package identitytest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// The OAuth client the fake provider accepts.
const (
	ClientID     = "test-client.apps.example"
	ClientSecret = "test-client-secret-ZQXJ"
	keyID        = "test-key"
)

// Grant is what one authorization code redeems to. The zero values of the override fields give a
// valid token.
type Grant struct {
	Subject       string
	Email         string
	EmailVerified bool
	Nonce         string
	CodeChallenge string // the S256 challenge of the authorization request

	Audience string          // "" = ClientID
	Issuer   string          // "" = the server's URL
	Expiry   time.Duration   // 0 = five minutes from now; negative = already expired
	SignWith *rsa.PrivateKey // nil = the published key
}

// Provider is a running fake provider. Safe for concurrent use.
type Provider struct {
	Server *httptest.Server

	mu         sync.Mutex
	key        *rsa.PrivateKey
	codes      map[string]Grant
	down       bool
	tokenCalls int
}

// New starts a provider that stops when the test ends.
func New(t testing.TB) *Provider {
	t.Helper()
	p := &Provider{key: NewKey(t), codes: map[string]Grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /keys", p.keys)
	mux.HandleFunc("POST /token", p.token)
	p.Server = httptest.NewServer(p.guard(mux))
	t.Cleanup(p.Server.Close)
	return p
}

// NewKey returns an RSA key, for example one the provider does not publish.
func NewKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// Issuer is the provider's issuer URL.
func (p *Provider) Issuer() string { return p.Server.URL }

// Authorize registers a one-time code that redeems to g and returns it.
func (p *Provider) Authorize(t testing.TB, g Grant) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.codes[code] = g
	return code
}

// SetDown makes every endpoint answer 503 (down) or work again.
func (p *Provider) SetDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
}

// TokenCalls is how many times the token endpoint was called.
func (p *Provider) TokenCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tokenCalls
}

// Challenge is the S256 PKCE challenge of verifier (RFC 7636).
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ParamsFromURL reads the flow values of an authorization URL.
func ParamsFromURL(t testing.TB, authURL string) (state, nonce, challenge string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("authorization URL: %v", err)
	}
	q := u.Query()
	return q.Get("state"), q.Get("nonce"), q.Get("code_challenge")
}

func (p *Provider) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		down := p.down
		p.mu.Unlock()
		if down {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.Issuer(),
		"authorization_endpoint":                p.Issuer() + "/auth",
		"token_endpoint":                        p.Issuer() + "/token",
		"jwks_uri":                              p.Issuer() + "/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *Provider) keys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: &p.key.PublicKey, KeyID: keyID, Algorithm: string(jose.RS256), Use: "sig",
	}}})
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.tokenCalls++
	p.mu.Unlock()
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, secret, ok := r.BasicAuth()
	if ok { // oauth2 query-escapes basic credentials
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != ClientID || secret != ClientSecret {
		tokenError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	g, found := p.codes[code]
	delete(p.codes, code) // a code works once
	p.mu.Unlock()
	if r.PostForm.Get("grant_type") != "authorization_code" || !found ||
		Challenge(r.PostForm.Get("code_verifier")) != g.CodeChallenge {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	raw, err := p.sign(g)
	if err != nil {
		http.Error(w, "signing failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": "fake-access-token", "token_type": "Bearer", "expires_in": 3600, "id_token": raw,
	})
}

func (p *Provider) sign(g Grant) (string, error) {
	key, aud, iss, life := p.key, g.Audience, g.Issuer, g.Expiry
	if g.SignWith != nil {
		key = g.SignWith
	}
	if aud == "" {
		aud = ClientID
	}
	if iss == "" {
		iss = p.Issuer()
	}
	if life == 0 {
		life = 5 * time.Minute
	}
	now := time.Now()
	payload, err := json.Marshal(map[string]any{
		"iss": iss, "aud": aud, "sub": g.Subject, "email": g.Email, "email_verified": g.EmailVerified,
		"nonce": g.Nonce, "iat": now.Unix(), "exp": now.Add(life).Unix(),
	})
	if err != nil {
		return "", err
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID))
	if err != nil {
		return "", err
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		return "", err
	}
	return obj.CompactSerialize()
}

func tokenError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
