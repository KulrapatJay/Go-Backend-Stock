package service

import (
	"context"
	"fmt"

	"go-backend-stock/internal/core/domain"
)

// admit checks rules before the use case does any work. A refused request becomes rate_limited with
// the wait; the first refusal per key per minute is also audited, in its own transaction (phase 1
// spec section 9). actor is the session-proven principal of the request, or the zero ID; it must
// never be a user looked up by email, because audit actors come only from proven identities.
func (a *Auth) admit(ctx context.Context, rules []domain.RateLimitRule, meta domain.RequestMeta, actor domain.ID) error {
	decision, err := a.store.RateLimits().Admit(ctx, rules, NowForStorage(a.clock))
	if err != nil {
		// A limiter outage is a dependency failure (503), like a failing session store.
		return fmt.Errorf("%w: %w", domain.NewError(domain.CodeDependencyUnavailable, "rate limiter is unavailable"), err)
	}
	if decision.Allowed {
		return nil
	}
	if decision.RecordDenialAudit {
		// A lost denial audit is logged by recordFailure; the client still gets its 429.
		_ = a.recordFailure(ctx, meta, auditSpec{
			Type: domain.EventSecurityRateLimited, Outcome: domain.AuditOutcomeDenied, Actor: actor,
			TargetType: "rate_limit", TargetID: string(decision.Scope), Scope: decision.Scope,
		})
	}
	return domain.NewRetryError(domain.CodeRateLimited, "too many requests, try again later", decision.RetryAfter)
}
