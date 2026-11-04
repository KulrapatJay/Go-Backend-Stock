package service

import (
	"context"
	"fmt"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// admit checks rules before the use case does any work. A refused request becomes rate_limited with
// the wait; the first refusal per key per minute is also audited, in its own transaction (phase 1
// spec section 9). actor is the session-proven principal of the request, or the zero ID; it must
// never be a user looked up by email, because audit actors come only from proven identities.
func (a *Auth) admit(ctx context.Context, rules []domain.RateLimitRule, meta domain.RequestMeta, actor domain.ID) error {
	return admitRules(ctx, a.store.RateLimits(), a.clock, rules, func(d domain.RateLimitDecision) {
		// A lost denial audit is logged by recordFailure; the client still gets its 429.
		_ = a.recordFailure(ctx, meta, auditSpec{
			Type: domain.EventSecurityRateLimited, Outcome: domain.AuditOutcomeDenied, Actor: actor,
			TargetType: "rate_limit", TargetID: string(d.Scope), Scope: d.Scope,
		})
	})
}

// admitRules asks the limiter about rules. A limiter failure is a dependency failure (503) with the
// cause kept; a refusal is rate_limited with the wait. onFirstDenial, when not nil, runs once for a
// refusal the limiter marks as the first of its minute, so the caller can audit it; it must not
// change the answer.
func admitRules(ctx context.Context, limits port.RateLimitRepository, clock port.Clock, rules []domain.RateLimitRule, onFirstDenial func(domain.RateLimitDecision)) error {
	decision, err := limits.Admit(ctx, rules, NowForStorage(clock))
	if err != nil {
		return fmt.Errorf("%w: %w", domain.NewError(domain.CodeDependencyUnavailable, "rate limiter is unavailable"), err)
	}
	if decision.Allowed {
		return nil
	}
	if decision.RecordDenialAudit && onFirstDenial != nil {
		onFirstDenial(decision)
	}
	return domain.NewRetryError(domain.CodeRateLimited, "too many requests, try again later", decision.RetryAfter)
}
