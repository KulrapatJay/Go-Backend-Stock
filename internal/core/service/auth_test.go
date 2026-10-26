package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestNewAuthValidatesDependencies(t *testing.T) {
	for name, mutate := range map[string]func(*AuthDeps){
		"no store":          func(d *AuthDeps) { d.Store = nil },
		"no unit of work":   func(d *AuthDeps) { d.UnitOfWork = nil },
		"no clock":          func(d *AuthDeps) { d.Clock = nil },
		"no hasher":         func(d *AuthDeps) { d.Hasher = nil },
		"no otp codec":      func(d *AuthDeps) { d.OTP = nil },
		"no rate limits":    func(d *AuthDeps) { d.Limits = nil },
		"no pacer":          func(d *AuthDeps) { d.Pacer = nil },
		"no mailer":         func(d *AuthDeps) { d.Mailer = nil },
		"no logger":         func(d *AuthDeps) { d.Logger = nil },
		"no session ttl":    func(d *AuthDeps) { d.SessionTTL = 0 },
		"no otp attempts":   func(d *AuthDeps) { d.OTPMaxAttempts = 0 },
		"too many attempts": func(d *AuthDeps) { d.OTPMaxAttempts = domain.MaxOTPFailedAttemptsCeiling + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			deps := validAuthDeps(t)
			mutate(&deps)
			if _, err := NewAuth(deps); err == nil {
				t.Error("NewAuth() accepted the dependencies")
			}
		})
	}
}

// validAuthDeps returns the dependencies newTestAuth uses, for tests that change one of them.
func validAuthDeps(t *testing.T) AuthDeps {
	t.Helper()
	var deps AuthDeps
	newTestAuth(t, func(d *AuthDeps) { deps = *d })
	return deps
}

func TestAuthenticate(t *testing.T) {
	ta := newTestAuth(t, nil)
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	now := NowForStorage(ta.clock)
	session := domain.Session{ID: mustID(t), UserID: mustID(t), TokenHash: hash, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	ta.repos.sessions.byHash[string(hash)] = session

	p, err := ta.auth.Authenticate(t.Context(), token)
	if err != nil || p.UserID != session.UserID || p.SessionID != session.ID {
		t.Fatalf("Authenticate(valid) = %+v, %v", p, err)
	}

	calls := ta.repos.sessions.calls
	_, err = ta.auth.Authenticate(t.Context(), "not-a-token")
	_ = requireCode(t, err, domain.CodeUnauthenticated)
	if ta.repos.sessions.calls != calls {
		t.Error("a malformed token must be refused without a database query")
	}

	other, _, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = ta.auth.Authenticate(t.Context(), other)
	_ = requireCode(t, err, domain.CodeUnauthenticated)

	ta.clock.Advance(time.Hour) // exactly at expires_at the session is over
	_, err = ta.auth.Authenticate(t.Context(), token)
	_ = requireCode(t, err, domain.CodeUnauthenticated)

	revokedAt := now
	session.RevokedAt, session.RevokeReason = &revokedAt, domain.RevokeReasonLogout
	session.ExpiresAt = now.Add(48 * time.Hour)
	ta.repos.sessions.byHash[string(hash)] = session
	_, err = ta.auth.Authenticate(t.Context(), token)
	_ = requireCode(t, err, domain.CodeUnauthenticated)

	ta.repos.sessions.err = errors.New("connection refused")
	_, err = ta.auth.Authenticate(t.Context(), token)
	_ = requireCode(t, err, domain.CodeDependencyUnavailable)
}

func TestAdmitPassesTheRulesAndStorageTime(t *testing.T) {
	ta := newTestAuth(t, nil)
	ta.clock.now = ta.clock.now.Add(123456789 * time.Nanosecond)
	rules, err := ta.auth.limits.EmailVerificationConfirm(testMeta(t).ClientIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := ta.auth.admit(t.Context(), rules, testMeta(t), domain.ID{}); err != nil {
		t.Fatalf("admit() error = %v", err)
	}
	if len(ta.repos.limits.rules) != len(rules) || !ta.repos.limits.at.Equal(NowForStorage(ta.clock)) {
		t.Errorf("Admit got %d rules at %v, want %d at %v", len(ta.repos.limits.rules), ta.repos.limits.at, len(rules), NowForStorage(ta.clock))
	}
	if len(ta.uow.audit.events) != 0 {
		t.Error("an allowed request must not write audit")
	}
}

func TestAdmitDenialReturnsRetryAfterAndAuditsWhenAsked(t *testing.T) {
	ta := newTestAuth(t, nil)
	actor := mustID(t)
	meta := testMeta(t)
	ta.repos.limits.decision = domain.RateLimitDecision{RetryAfter: 90 * time.Second, RecordDenialAudit: true, Scope: domain.RateLimitLoginEmail}

	err := ta.auth.admit(t.Context(), nil, meta, actor)
	de := requireCode(t, err, domain.CodeRateLimited)
	if de.RetryAfter != 90*time.Second {
		t.Errorf("RetryAfter = %v, want 90s", de.RetryAfter)
	}
	if len(ta.uow.audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(ta.uow.audit.events))
	}
	e := ta.uow.audit.events[0]
	if e.EventType != domain.EventSecurityRateLimited || e.Outcome != domain.AuditOutcomeDenied || e.ActorUserID != actor ||
		e.RequestID != meta.RequestID || e.TargetType != "rate_limit" || e.TargetID != string(domain.RateLimitLoginEmail) {
		t.Errorf("audit event = %+v", e)
	}
	var md map[string]string
	if err := json.Unmarshal(e.Metadata, &md); err != nil || md["scope"] != "login_email" {
		t.Errorf("metadata %s must name the scope", e.Metadata)
	}

	ta.repos.limits.decision.RecordDenialAudit = false
	_ = ta.auth.admit(t.Context(), nil, meta, actor)
	if len(ta.uow.audit.events) != 1 {
		t.Error("a denial that is not flagged for audit must not write one (one per key per minute)")
	}
}

func TestAuditMetadataKeepsOnlyTheAllowlist(t *testing.T) {
	ta := newTestAuth(t, nil)
	meta := testMeta(t)
	e, err := ta.auth.newAuditEvent(meta, auditSpec{
		Type: domain.EventAuthLoginFailed, Outcome: domain.AuditOutcomeFailure, TargetType: "login",
		Reason: "invalid_credentials", Method: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	var md map[string]any
	if err := json.Unmarshal(e.Metadata, &md); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"ip": "203.0.113.0/24", "user_agent": "test-agent/1.0", "method": "password"}
	if len(md) != len(want) {
		t.Errorf("metadata = %v, want exactly %v", md, want)
	}
	for k, v := range want {
		if md[k] != v {
			t.Errorf("metadata[%s] = %v, want %v", k, md[k], v)
		}
	}
	if strings.Contains(string(e.Metadata), "203.0.113.9") {
		t.Error("metadata must hold the masked IP, never the full one")
	}
	if !e.ActorUserID.IsZero() || e.ReasonCode != "invalid_credentials" || !e.OccurredAt.Equal(NowForStorage(ta.clock)) {
		t.Errorf("event = %+v", e)
	}
}

func TestRecordFailureLogsWhenTheAuditCannotBeWritten(t *testing.T) {
	ta := newTestAuth(t, nil)
	ta.uow.audit.err = errors.New("disk full")
	err := ta.auth.recordFailure(t.Context(), testMeta(t), auditSpec{
		Type: domain.EventAuthLoginFailed, Outcome: domain.AuditOutcomeFailure, TargetType: "login",
	})
	_ = requireCode(t, err, domain.CodeAuditWriteFailed)
	if !strings.Contains(ta.logs.String(), `"event":"audit.write_failed"`) || !strings.Contains(ta.logs.String(), `"level":"ERROR"`) {
		t.Errorf("log %q should record audit.write_failed at error level", ta.logs.String())
	}
}
