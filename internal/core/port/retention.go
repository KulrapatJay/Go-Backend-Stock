package port

import (
	"context"
	"time"
)

// RetentionRepository deletes data that outlived its purpose. Every method deletes at most limit
// rows older than before in one statement and returns how many it deleted; a result below limit
// means nothing older is left. It is implemented with the maintenance database role, which can
// read only the expiry columns and delete, so it can never read or change what the API stores.
type RetentionRepository interface {
	// DeleteAuditEventsBefore deletes audit events that occurred before the cutoff.
	DeleteAuditEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error)
	// DeleteIdempotencyKeysExpiredBefore deletes idempotency records that expired before the cutoff.
	DeleteIdempotencyKeysExpiredBefore(ctx context.Context, before time.Time, limit int) (int64, error)
	// DeleteSessionsExpiredBefore deletes sessions that expired before the cutoff.
	DeleteSessionsExpiredBefore(ctx context.Context, before time.Time, limit int) (int64, error)
	// DeleteOTPChallengesExpiredBefore deletes OTP challenges that expired before the cutoff.
	DeleteOTPChallengesExpiredBefore(ctx context.Context, before time.Time, limit int) (int64, error)
	// DeleteOIDCFlowsExpiredBefore deletes OpenID Connect flows that expired before the cutoff.
	DeleteOIDCFlowsExpiredBefore(ctx context.Context, before time.Time, limit int) (int64, error)
	// DeleteRateLimitEventsBefore deletes rate-limit events recorded before the cutoff.
	DeleteRateLimitEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error)
	// DeleteIdleRateLimitKeys deletes rate-limit keys that expired before the cutoff and have no
	// event left. A key the API starts using at the same moment is skipped, not an error: it is
	// deleted by a later run.
	DeleteIdleRateLimitKeys(ctx context.Context, before time.Time, limit int) (int64, error)
}
