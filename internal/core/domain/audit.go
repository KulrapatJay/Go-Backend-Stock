package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxAuditMetadataBytes   = 8 * 1024
	maxAuditTargetTypeRunes = 40
	maxAuditReasonCodeRunes = 80
)

// AuditOutcome is the result an audit event records (audit_events.outcome CHECK).
type AuditOutcome string

const (
	AuditOutcomeSuccess AuditOutcome = "success"
	AuditOutcomeFailure AuditOutcome = "failure"
	AuditOutcomeDenied  AuditOutcome = "denied"
)

// EventType is one of the phase 1 audit events listed in AGENTS.md.
type EventType string

const (
	EventAuthRegistered                 EventType = "auth.registered"
	EventAuthLoginSucceeded             EventType = "auth.login_succeeded"
	EventAuthLoginFailed                EventType = "auth.login_failed"
	EventAuthEmailVerificationRequested EventType = "auth.email_verification_requested"
	EventAuthEmailVerified              EventType = "auth.email_verified"
	EventAuthEmailVerificationFailed    EventType = "auth.email_verification_failed"
	EventAuthLogout                     EventType = "auth.logout"
	EventAuthSessionsRevoked            EventType = "auth.sessions_revoked"
	EventAuthPasswordResetRequested     EventType = "auth.password_reset_requested"
	EventAuthOTPVerificationFailed      EventType = "auth.otp_verification_failed"
	EventAuthPasswordResetSucceeded     EventType = "auth.password_reset_succeeded"
	EventAuthIdentityLinked             EventType = "auth.identity_linked"
	EventAuthIdentityLinkFailed         EventType = "auth.identity_link_failed"
	EventPortfolioCreated               EventType = "portfolio.created"
	EventPortfolioUpdated               EventType = "portfolio.updated"
	EventPortfolioDeleted               EventType = "portfolio.deleted"
	EventTransactionCreated             EventType = "transaction.created"
	EventTransactionUpdated             EventType = "transaction.updated"
	EventTransactionDeleted             EventType = "transaction.deleted"
	EventSecurityAccessDenied           EventType = "security.access_denied"
	EventSecurityRateLimited            EventType = "security.rate_limited"
)

// EventTypes lists every audit event type, for tests and the DB enum check in milestone 3.
func EventTypes() []EventType {
	return []EventType{
		EventAuthRegistered, EventAuthLoginSucceeded, EventAuthLoginFailed,
		EventAuthEmailVerificationRequested, EventAuthEmailVerified, EventAuthEmailVerificationFailed,
		EventAuthLogout, EventAuthSessionsRevoked,
		EventAuthPasswordResetRequested, EventAuthOTPVerificationFailed, EventAuthPasswordResetSucceeded,
		EventAuthIdentityLinked, EventAuthIdentityLinkFailed,
		EventPortfolioCreated, EventPortfolioUpdated, EventPortfolioDeleted,
		EventTransactionCreated, EventTransactionUpdated, EventTransactionDeleted,
		EventSecurityAccessDenied, EventSecurityRateLimited,
	}
}

// AuditEvent is an append-only record of something a use case did. Optional IDs use the zero
// ID; optional strings use "". Metadata must be a JSON object; nil means "{}".
type AuditEvent struct {
	ID          ID
	OccurredAt  time.Time
	EventType   EventType
	ActorUserID ID // zero when the actor is not authenticated, e.g. a failed login
	RequestID   ID
	SessionID   ID // internal session ID, never the session token or its hash
	TargetType  string
	TargetID    string
	Outcome     AuditOutcome
	ReasonCode  string
	Metadata    json.RawMessage
}

// NewAuditEvent checks an event before it is appended and replaces nil metadata with "{}".
func NewAuditEvent(e AuditEvent) (AuditEvent, error) {
	if e.Metadata == nil {
		e.Metadata = json.RawMessage("{}")
	}
	err := firstError(
		requireID("audit.id", e.ID),
		requireID("audit.request_id", e.RequestID),
	)
	switch {
	case err != nil:
	case e.OccurredAt.IsZero():
		err = invariant("audit.occurred_at", "is required")
	case !isOneOf(e.EventType, EventTypes()...):
		err = invariant("audit.event_type", "must be a phase 1 event type")
	case !isOneOf(e.Outcome, AuditOutcomeSuccess, AuditOutcomeFailure, AuditOutcomeDenied):
		err = invariant("audit.outcome", "must be success, failure or denied")
	case !isText(e.TargetType, 1, maxAuditTargetTypeRunes):
		err = invariant("audit.target_type", "must be 1-40 printable characters")
	case e.ReasonCode != "" && !isText(e.ReasonCode, 1, maxAuditReasonCodeRunes):
		err = invariant("audit.reason_code", "must be at most 80 printable characters")
	case len(e.Metadata) > MaxAuditMetadataBytes || !isJSONObject(e.Metadata):
		err = invariant("audit.metadata", "must be a JSON object of at most 8 KiB that PostgreSQL jsonb can store")
	}
	if err != nil {
		return AuditEvent{}, err
	}
	return e, nil
}

func isJSONObject(b json.RawMessage) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && t[0] == '{' && isStorableJSON(t)
}

// isStorableJSON reports whether b is JSON that PostgreSQL jsonb accepts: valid JSON, valid UTF-8
// (encoding/json would silently replace bad bytes) and no NUL character in any string or key
// (jsonb rejects the \u0000 escape).
func isStorableJSON(b []byte) bool {
	if !utf8.Valid(b) || !json.Valid(b) {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return false
	}
	return !containsNUL(v)
}

func containsNUL(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.IndexByte(x, 0) >= 0
	case []any:
		for _, item := range x {
			if containsNUL(item) {
				return true
			}
		}
	case map[string]any:
		for k, item := range x {
			if strings.IndexByte(k, 0) >= 0 || containsNUL(item) {
				return true
			}
		}
	}
	return false
}
