package domain

import "time"

// RateLimitScope is what a rate-limit key counts (rate_limit_keys.scope CHECK).
type RateLimitScope string

const (
	RateLimitLoginEmail                    RateLimitScope = "login_email"
	RateLimitLoginIP                       RateLimitScope = "login_ip"
	RateLimitSignupIP                      RateLimitScope = "signup_ip"
	RateLimitGoogleStartIP                 RateLimitScope = "google_start_ip"
	RateLimitEmailVerificationRequestEmail RateLimitScope = "email_verification_request_email"
	RateLimitEmailVerificationRequestIP    RateLimitScope = "email_verification_request_ip"
	RateLimitEmailVerificationConfirmIP    RateLimitScope = "email_verification_confirm_ip"
	RateLimitPasswordResetRequestEmail     RateLimitScope = "password_reset_request_email"
	RateLimitPasswordResetRequestIP        RateLimitScope = "password_reset_request_ip"
	RateLimitPasswordResetConfirmIP        RateLimitScope = "password_reset_confirm_ip"
	RateLimitInstrumentLookupUser          RateLimitScope = "instrument_lookup_user"
)

// RateLimitScopes lists every scope, for tests and the DB CHECK in milestone 3.
func RateLimitScopes() []RateLimitScope {
	return []RateLimitScope{
		RateLimitLoginEmail, RateLimitLoginIP, RateLimitSignupIP, RateLimitGoogleStartIP,
		RateLimitEmailVerificationRequestEmail, RateLimitEmailVerificationRequestIP, RateLimitEmailVerificationConfirmIP,
		RateLimitPasswordResetRequestEmail, RateLimitPasswordResetRequestIP, RateLimitPasswordResetConfirmIP,
		RateLimitInstrumentLookupUser,
	}
}

// RateLimitRule allows at most Limit events per Window for one hashed key.
type RateLimitRule struct {
	Scope   RateLimitScope
	KeyHash []byte
	Limit   int
	Window  time.Duration
}

// NewRateLimitRule checks a rule built from config.
func NewRateLimitRule(r RateLimitRule) (RateLimitRule, error) {
	err := requireHash("rate_limit.key_hash", r.KeyHash)
	switch {
	case err != nil:
	case !isOneOf(r.Scope, RateLimitScopes()...):
		err = invariant("rate_limit.scope", "must be a known scope")
	case r.Limit < 1:
		err = invariant("rate_limit.limit", "must be at least 1")
	case r.Window <= 0:
		err = invariant("rate_limit.window", "must be positive")
	}
	if err != nil {
		return RateLimitRule{}, err
	}
	return r, nil
}

// RateLimitDecision is the result of admitting one request against a set of rules.
type RateLimitDecision struct {
	Allowed           bool
	RetryAfter        time.Duration
	RecordDenialAudit bool // true at most once per key per minute, so attacks cannot flood audit
	// Scope is the scope of the key that denied, for the security.rate_limited audit metadata;
	// "" when the request was allowed.
	Scope RateLimitScope
}
