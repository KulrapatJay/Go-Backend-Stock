package port

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
)

// AuditRepository appends audit events. There is no update or delete: audit is append-only for
// the runtime role.
type AuditRepository interface {
	Append(ctx context.Context, event domain.AuditEvent) error
}

// RateLimitRepository counts attempts in PostgreSQL so a restart cannot reset quotas.
type RateLimitRepository interface {
	// Admit checks every rule in one transaction. When all allow, it records one event per key.
	// When any denies, it records nothing and creates no key after the first denying one
	// (client-IP keys are checked first, so an IP over its quota cannot create email keys).
	Admit(ctx context.Context, rules []domain.RateLimitRule, now time.Time) (domain.RateLimitDecision, error)
}
