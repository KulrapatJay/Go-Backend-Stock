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
	// Admit atomically records one attempt against every rule and reports whether all allow it.
	Admit(ctx context.Context, rules []domain.RateLimitRule, now time.Time) (domain.RateLimitDecision, error)
}
