package domain

import (
	"time"
	"unicode/utf8"
)

const maxProviderSubjectRunes = 255

// User is an account. It deliberately has no password hash field: only the login, register and
// reset queries read or write users.password_hash, so the hash cannot leak through a User.
type User struct {
	ID              ID
	Email           Email
	EmailVerifiedAt *time.Time // nil until the email is verified; never cleared afterwards
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PasswordHash is a stored password hash (an Argon2id PHC string). It prints, formats and
// marshals as [REDACTED] so it cannot leak through logs; string(h) returns the real value for
// verification and storage only.
type PasswordHash string

const redacted = "[REDACTED]"

func (PasswordHash) String() string   { return redacted }
func (PasswordHash) GoString() string { return redacted }

// MarshalJSON keeps the hash out of JSON logs and responses.
func (PasswordHash) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// UserCredentials is the only value that carries a user's password hash. It is returned solely by
// UserRepository.CredentialsByEmail for login, register and password reset.
type UserCredentials struct {
	User         User
	PasswordHash PasswordHash // "" for accounts that sign in only with Google
}

// HasPassword reports whether the account can sign in with a password.
func (c UserCredentials) HasPassword() bool { return c.PasswordHash != "" }

// RevokeReason records why a session was revoked (sessions.revoke_reason CHECK).
type RevokeReason string

const (
	RevokeReasonLogout        RevokeReason = "logout"
	RevokeReasonPasswordReset RevokeReason = "password_reset"
)

// ParseRevokeReason accepts exactly "logout" or "password_reset".
func ParseRevokeReason(s string) (RevokeReason, error) {
	return parseEnum("revoke_reason", s, RevokeReasonLogout, RevokeReasonPasswordReset)
}

// Session is a server-side login session. Only the SHA-256 hash of its token is stored.
type Session struct {
	ID           ID
	UserID       ID
	TokenHash    []byte
	CreatedAt    time.Time
	ExpiresAt    time.Time
	RevokedAt    *time.Time
	RevokeReason RevokeReason // "" while not revoked
}

// NewSession checks a session the service is about to create: not yet revoked.
func NewSession(s Session) (Session, error) {
	err := firstError(
		requireID("session.id", s.ID),
		requireID("session.user_id", s.UserID),
		requireHash("session.token_hash", s.TokenHash),
		requireExpiry(s.CreatedAt, s.ExpiresAt),
	)
	if err == nil && (s.RevokedAt != nil || s.RevokeReason != "") {
		err = invariant("session", "must not be revoked when created")
	}
	if err != nil {
		return Session{}, err
	}
	return s, nil
}

// IdentityProviderName is an external identity provider (auth_providers.provider CHECK).
type IdentityProviderName string

const IdentityProviderGoogle IdentityProviderName = "google"

// ParseIdentityProviderName accepts exactly "google".
func ParseIdentityProviderName(s string) (IdentityProviderName, error) {
	return parseEnum("provider", s, IdentityProviderGoogle)
}

// AuthProvider links a user to an external identity. ProviderSubject is the provider's stable
// subject (Google "sub"), never the email.
type AuthProvider struct {
	ID              ID
	UserID          ID
	Provider        IdentityProviderName
	ProviderSubject string
	CreatedAt       time.Time
}

// NewAuthProvider checks a link the service is about to store.
func NewAuthProvider(p AuthProvider) (AuthProvider, error) {
	err := firstError(
		requireID("auth_provider.id", p.ID),
		requireID("auth_provider.user_id", p.UserID),
	)
	switch {
	case err != nil:
	case !isOneOf(p.Provider, IdentityProviderGoogle):
		err = invariant("auth_provider.provider", "must be google")
	case p.ProviderSubject == "" || !utf8.ValidString(p.ProviderSubject) ||
		utf8.RuneCountInString(p.ProviderSubject) > maxProviderSubjectRunes || hasControl(p.ProviderSubject):
		err = invariant("auth_provider.provider_subject", "must be 1-255 printable characters")
	case p.CreatedAt.IsZero():
		err = invariant("auth_provider.created_at", "is required")
	}
	if err != nil {
		return AuthProvider{}, err
	}
	return p, nil
}

// OIDCPurpose says what an OpenID Connect flow is for (oauth_flows.purpose CHECK).
type OIDCPurpose string

const (
	OIDCPurposeLogin OIDCPurpose = "login"
	OIDCPurposeLink  OIDCPurpose = "link"
)

// ParseOIDCPurpose accepts exactly "login" or "link".
func ParseOIDCPurpose(s string) (OIDCPurpose, error) {
	return parseEnum("purpose", s, OIDCPurposeLogin, OIDCPurposeLink)
}

// OIDCFlow is a pending Google sign-in or account link. A link flow belongs to the signed-in
// user; a login flow has no user (UserID is the zero ID).
type OIDCFlow struct {
	ID                 ID
	Purpose            OIDCPurpose
	UserID             ID
	StateHash          []byte
	NonceHash          []byte
	BrowserBindingHash []byte
	CreatedAt          time.Time
	ExpiresAt          time.Time
	ConsumedAt         *time.Time
}

// NewOIDCFlow checks a flow the service is about to store: unconsumed, with the user rule above.
func NewOIDCFlow(f OIDCFlow) (OIDCFlow, error) {
	err := firstError(
		requireID("oidc_flow.id", f.ID),
		requireHash("oidc_flow.state_hash", f.StateHash),
		requireHash("oidc_flow.nonce_hash", f.NonceHash),
		requireHash("oidc_flow.browser_binding_hash", f.BrowserBindingHash),
		requireExpiry(f.CreatedAt, f.ExpiresAt),
	)
	switch {
	case err != nil:
	case f.Purpose == OIDCPurposeLink && f.UserID.IsZero():
		err = invariant("oidc_flow.user_id", "is required for a link flow")
	case f.Purpose == OIDCPurposeLogin && !f.UserID.IsZero():
		err = invariant("oidc_flow.user_id", "must be empty for a login flow")
	case !isOneOf(f.Purpose, OIDCPurposeLogin, OIDCPurposeLink):
		err = invariant("oidc_flow.purpose", "must be login or link")
	case f.ConsumedAt != nil:
		err = invariant("oidc_flow", "must not be consumed when created")
	}
	if err != nil {
		return OIDCFlow{}, err
	}
	return f, nil
}
