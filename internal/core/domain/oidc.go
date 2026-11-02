package domain

import "time"

// OIDCOutcome is where the Google callback sends the browser: /docs/?google=<outcome>.
type OIDCOutcome string

const (
	OIDCOutcomeSuccess      OIDCOutcome = "success"       // signed in; the session cookie is set
	OIDCOutcomeLinked       OIDCOutcome = "linked"        // Google added to the signed-in account
	OIDCOutcomeLinkRequired OIDCOutcome = "link_required" // the email has an account: sign in to it, then link
	OIDCOutcomeFailed       OIDCOutcome = "failed"
)

// OIDCStart begins a Google flow. Principal is the signed-in caller: required to link, ignored for
// login.
type OIDCStart struct {
	Purpose   OIDCPurpose
	Principal Principal
}

// OIDCRedirect is where to send the browser, and the browser binding the HTTP adapter keeps in an
// HttpOnly cookie until the callback. Only the binding's hash is stored.
type OIDCRedirect struct {
	URL            string
	BrowserBinding SecretText
	ExpiresAt      time.Time
}

// OIDCCallback is what came back: the callback query, the binding cookie, and the caller's current
// session (zero when there is none).
type OIDCCallback struct {
	Code           SecretText
	State          string
	ProviderError  string // the provider's "error" query value; never shown to the client
	BrowserBinding SecretText
	Principal      Principal
}

// OIDCResult ends a flow. Session is set only when Outcome is OIDCOutcomeSuccess.
type OIDCResult struct {
	Outcome OIDCOutcome
	Session SessionGrant
}

// AuthorizationRequest is what the identity adapter puts in the provider's sign-in URL. The redirect
// URI and client ID are adapter configuration.
type AuthorizationRequest struct {
	State         string
	Nonce         string
	CodeChallenge string // S256 of the PKCE verifier (RFC 7636)
}

// CodeExchange redeems an authorization code with its PKCE verifier.
type CodeExchange struct {
	Code         SecretText
	CodeVerifier SecretText
}

// VerifiedIdentity is an ID token whose signature, issuer, audience and expiry the adapter checked.
// The service still compares Nonce with the flow. Email is as the provider sent it: parse it with
// ParseEmail before use.
type VerifiedIdentity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Nonce         string
}
