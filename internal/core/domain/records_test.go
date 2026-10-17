package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestParseIdempotencyKey(t *testing.T) {
	for _, k := range []string{"a", "0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d", strings.Repeat("k", 128), "!~"} {
		if got, err := domain.ParseIdempotencyKey(k); err != nil || got != k {
			t.Errorf("ParseIdempotencyKey(%q) = %q, %v", k, got, err)
		}
	}
	for _, k := range []string{"", strings.Repeat("k", 129), "has space", "tab\t", "ключ", "nul\x00"} {
		_, err := domain.ParseIdempotencyKey(k)
		requireInvalidInput(t, err, "Idempotency-Key")
	}
}

func validRecord(t *testing.T) domain.IdempotencyRecord {
	return domain.IdempotencyRecord{
		Scope:       domain.IdempotencyScope{UserID: mustID(t), PortfolioID: mustID(t), Operation: domain.IdempotencyOperationCreateTransaction},
		Key:         "retry-1",
		RequestHash: hash32,
		Result:      json.RawMessage(`{"id":"x"}`),
		CreatedAt:   t0,
		ExpiresAt:   t0.Add(24 * time.Hour),
	}
}

func TestNewIdempotencyRecord(t *testing.T) {
	if _, err := domain.NewIdempotencyRecord(validRecord(t)); err != nil {
		t.Fatalf("NewIdempotencyRecord(valid) error = %v", err)
	}
	big := validRecord(t)
	big.Result = json.RawMessage(`"` + strings.Repeat("a", domain.MaxIdempotencyResultBytes-2) + `"`)
	if _, err := domain.NewIdempotencyRecord(big); err != nil {
		t.Errorf("16 KiB result rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.IdempotencyRecord){
		"no portfolio":  func(r *domain.IdempotencyRecord) { r.Scope.PortfolioID = domain.ID{} },
		"bad operation": func(r *domain.IdempotencyRecord) { r.Scope.Operation = "portfolios.create" },
		"empty key":     func(r *domain.IdempotencyRecord) { r.Key = "" },
		"short hash":    func(r *domain.IdempotencyRecord) { r.RequestHash = hash31 },
		"invalid json":  func(r *domain.IdempotencyRecord) { r.Result = json.RawMessage(`{"id":`) },
		"result too big": func(r *domain.IdempotencyRecord) {
			r.Result = json.RawMessage(`"` + strings.Repeat("a", domain.MaxIdempotencyResultBytes) + `"`)
		},
		"no expiry": func(r *domain.IdempotencyRecord) { r.ExpiresAt = r.CreatedAt },
	} {
		t.Run(name, func(t *testing.T) {
			r := validRecord(t)
			mutate(&r)
			_, err := domain.NewIdempotencyRecord(r)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}

func validAudit(t *testing.T) domain.AuditEvent {
	return domain.AuditEvent{
		ID: mustID(t), OccurredAt: t0, EventType: domain.EventPortfolioCreated, ActorUserID: mustID(t),
		RequestID: mustID(t), TargetType: "portfolio", TargetID: "0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d",
		Outcome: domain.AuditOutcomeSuccess, Metadata: json.RawMessage(`{"name":"Long term"}`),
	}
}

func TestNewAuditEvent(t *testing.T) {
	e, err := domain.NewAuditEvent(validAudit(t))
	if err != nil {
		t.Fatalf("NewAuditEvent(valid) error = %v", err)
	}
	if string(e.Metadata) != `{"name":"Long term"}` {
		t.Errorf("metadata changed: %s", e.Metadata)
	}

	anon := validAudit(t)
	anon.EventType, anon.ActorUserID, anon.Outcome, anon.ReasonCode, anon.Metadata =
		domain.EventAuthLoginFailed, domain.ID{}, domain.AuditOutcomeFailure, "invalid_credentials", nil
	e, err = domain.NewAuditEvent(anon)
	if err != nil {
		t.Fatalf("failed login without actor rejected: %v", err)
	}
	if string(e.Metadata) != "{}" {
		t.Errorf("nil metadata = %s, want {}", e.Metadata)
	}

	big := validAudit(t)
	big.Metadata = json.RawMessage(`{"a":"` + strings.Repeat("x", domain.MaxAuditMetadataBytes-8) + `"}`)
	if _, err := domain.NewAuditEvent(big); err != nil {
		t.Errorf("8 KiB metadata rejected: %v", err)
	}

	for name, mutate := range map[string]func(*domain.AuditEvent){
		"no request id":     func(e *domain.AuditEvent) { e.RequestID = domain.ID{} },
		"no occurred_at":    func(e *domain.AuditEvent) { e.OccurredAt = time.Time{} },
		"unknown event":     func(e *domain.AuditEvent) { e.EventType = "portfolio.viewed" },
		"unknown outcome":   func(e *domain.AuditEvent) { e.Outcome = "ok" },
		"empty target type": func(e *domain.AuditEvent) { e.TargetType = "" },
		"long target type":  func(e *domain.AuditEvent) { e.TargetType = strings.Repeat("t", 41) },
		"long reason code":  func(e *domain.AuditEvent) { e.ReasonCode = strings.Repeat("r", 81) },
		"metadata array":    func(e *domain.AuditEvent) { e.Metadata = json.RawMessage(`["a"]`) },
		"metadata invalid":  func(e *domain.AuditEvent) { e.Metadata = json.RawMessage(`{"a":`) },
		"metadata too big": func(e *domain.AuditEvent) {
			e.Metadata = json.RawMessage(`{"a":"` + strings.Repeat("x", domain.MaxAuditMetadataBytes) + `"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := validAudit(t)
			mutate(&e)
			_, err := domain.NewAuditEvent(e)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}

func TestAuditEventTypesMatchAgentsList(t *testing.T) {
	types := domain.EventTypes()
	if len(types) != 21 {
		t.Fatalf("EventTypes() has %d types, AGENTS.md lists 21", len(types))
	}
	seen := map[domain.EventType]bool{}
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate event type %q", et)
		}
		seen[et] = true
		if len(et) > 80 {
			t.Errorf("event type %q is longer than varchar(80)", et)
		}
	}
}

func TestNewRateLimitRule(t *testing.T) {
	valid := func() domain.RateLimitRule {
		return domain.RateLimitRule{Scope: domain.RateLimitLoginEmail, KeyHash: hash32, Limit: 5, Window: time.Hour}
	}
	if _, err := domain.NewRateLimitRule(valid()); err != nil {
		t.Fatalf("NewRateLimitRule(valid) error = %v", err)
	}
	if n := len(domain.RateLimitScopes()); n != 11 {
		t.Errorf("RateLimitScopes() has %d scopes, spec section 7 lists 11", n)
	}
	for name, mutate := range map[string]func(*domain.RateLimitRule){
		"unknown scope": func(r *domain.RateLimitRule) { r.Scope = "login" },
		"short hash":    func(r *domain.RateLimitRule) { r.KeyHash = hash31 },
		"zero limit":    func(r *domain.RateLimitRule) { r.Limit = 0 },
		"zero window":   func(r *domain.RateLimitRule) { r.Window = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid()
			mutate(&r)
			_, err := domain.NewRateLimitRule(r)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}
