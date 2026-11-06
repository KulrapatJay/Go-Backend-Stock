package service

import (
	"context"
	"fmt"
	"time"

	"go-backend-stock/internal/core/port"
)

// Expired authentication state is kept this long after it expires, and rate-limit events this long
// after they happened (the longest rate-limit window is one hour).
const (
	expiredStateGrace  = 24 * time.Hour
	rateLimitEventKeep = time.Hour
)

// RetentionPolicy is how much history the maintenance job keeps.
type RetentionPolicy struct {
	// AuditRetention is how long audit events are kept.
	AuditRetention time.Duration
	// BatchSize is the most rows one delete statement removes.
	BatchSize int
}

// RetentionReport counts the rows deleted from each table.
type RetentionReport struct {
	AuditEvents     int64
	IdempotencyKeys int64
	Sessions        int64
	OTPChallenges   int64
	OIDCFlows       int64
	RateLimitEvents int64
	RateLimitKeys   int64
}

// Retention deletes expired data in batches. It records no audit event of its own: its role has
// no right to write one, so the caller reports the RetentionReport in the application log.
type Retention struct {
	repo   port.RetentionRepository
	clock  port.Clock
	policy RetentionPolicy
}

// NewRetention returns a Retention that reads the time from clock once per Run.
func NewRetention(repo port.RetentionRepository, clock port.Clock, policy RetentionPolicy) *Retention {
	return &Retention{repo: repo, clock: clock, policy: policy}
}

type retentionStep struct {
	table  string
	before time.Time
	delete func(ctx context.Context, before time.Time, limit int) (int64, error)
	count  *int64
}

// Run deletes everything past its retention, table by table, and returns what it deleted. It stops
// at the first error or when ctx ends, returning the counts reached so far. Rate-limit events go
// before rate-limit keys because a key with an event cannot be deleted.
func (r *Retention) Run(ctx context.Context) (RetentionReport, error) {
	var report RetentionReport
	if r.policy.BatchSize < 1 {
		return report, fmt.Errorf("retention batch size must be at least 1, got %d", r.policy.BatchSize)
	}
	now := r.clock.Now().UTC()
	steps := []retentionStep{
		{"audit_events", now.Add(-r.policy.AuditRetention), r.repo.DeleteAuditEventsBefore, &report.AuditEvents},
		{"idempotency_keys", now, r.repo.DeleteIdempotencyKeysExpiredBefore, &report.IdempotencyKeys},
		{"sessions", now.Add(-expiredStateGrace), r.repo.DeleteSessionsExpiredBefore, &report.Sessions},
		{"otp_challenges", now.Add(-expiredStateGrace), r.repo.DeleteOTPChallengesExpiredBefore, &report.OTPChallenges},
		{"oauth_flows", now.Add(-expiredStateGrace), r.repo.DeleteOIDCFlowsExpiredBefore, &report.OIDCFlows},
		{"rate_limit_events", now.Add(-rateLimitEventKeep), r.repo.DeleteRateLimitEventsBefore, &report.RateLimitEvents},
		{"rate_limit_keys", now, r.repo.DeleteIdleRateLimitKeys, &report.RateLimitKeys},
	}
	for _, step := range steps {
		for {
			if err := ctx.Err(); err != nil {
				return report, fmt.Errorf("retention %s: %w", step.table, err)
			}
			n, err := step.delete(ctx, step.before, r.policy.BatchSize)
			*step.count += n
			if err != nil {
				return report, fmt.Errorf("retention %s: %w", step.table, err)
			}
			if n < int64(r.policy.BatchSize) {
				break
			}
		}
	}
	return report, nil
}
