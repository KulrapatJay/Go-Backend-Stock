package postgres

import (
	"bytes"
	"context"
	"slices"
	"time"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// denialAuditInterval is how often a key may report a denial for a security.rate_limited audit
// event (phase 1 spec section 9: one event per key per minute).
const denialAuditInterval = time.Minute

// RateLimitRepository admits requests against rolling-window limits stored in PostgreSQL, so a
// restart cannot reset a quota.
type RateLimitRepository struct {
	store *Store
}

var _ port.RateLimitRepository = (*RateLimitRepository)(nil)

type rateLimitKey struct {
	hash      []byte
	scope     domain.RateLimitScope
	maxWindow time.Duration
}

// Admit checks every rule in one transaction. When all allow the request it records one event per
// key; when any denies it records nothing (admission limit: denied attempts do not extend the
// wait), reports when the request would fit, and sets RecordDenialAudit for the first denial of a
// key in a minute. now comes from the service's Clock, never from the database.
func (r *RateLimitRepository) Admit(ctx context.Context, rules []domain.RateLimitRule, now time.Time) (domain.RateLimitDecision, error) {
	keys, err := rateLimitKeys(rules)
	if err != nil {
		return domain.RateLimitDecision{}, err
	}
	var decision domain.RateLimitDecision
	err = r.store.withinTx(ctx, func(tx *txRepositories) error {
		// Upserting each key takes its row lock; keys are always locked in key_hash order so two
		// requests that share several keys cannot deadlock.
		for _, k := range keys {
			if err := tx.q.UpsertRateLimitKey(ctx, sqlc.UpsertRateLimitKeyParams{
				KeyHash: k.hash, Scope: string(k.scope), ExpiresAt: pgTime(now.Add(k.maxWindow)),
			}); err != nil {
				return mapError(err)
			}
		}

		var denied [][]byte
		var retryAfter time.Duration
		for _, rule := range rules {
			cutoff := pgTime(now.Add(-rule.Window))
			count, err := tx.q.CountRateLimitEvents(ctx, sqlc.CountRateLimitEventsParams{KeyHash: rule.KeyHash, OccurredAt: cutoff})
			if err != nil {
				return mapError(err)
			}
			if count < int64(rule.Limit) {
				continue
			}
			// One more attempt fits once the (count-limit)-th oldest event leaves the window.
			oldest, err := tx.q.NthRateLimitEventTime(ctx, sqlc.NthRateLimitEventTimeParams{
				KeyHash: rule.KeyHash, OccurredAt: cutoff, Offset: int32(count - int64(rule.Limit)), //nolint:gosec // count is a row count of one key's window, far below MaxInt32.
			})
			if err != nil {
				return mapError(err)
			}
			retryAfter = max(retryAfter, timeFromPG(oldest).Add(rule.Window).Sub(now))
			if !slices.ContainsFunc(denied, func(h []byte) bool { return bytes.Equal(h, rule.KeyHash) }) {
				denied = append(denied, rule.KeyHash)
			}
		}

		if len(denied) == 0 {
			for _, k := range keys {
				if err := tx.q.InsertRateLimitEvent(ctx, sqlc.InsertRateLimitEventParams{KeyHash: k.hash, OccurredAt: pgTime(now)}); err != nil {
					return mapError(err)
				}
			}
			decision = domain.RateLimitDecision{Allowed: true}
			return nil
		}

		decision = domain.RateLimitDecision{RetryAfter: retryAfter}
		for _, hash := range denied {
			claimed, err := tx.q.ClaimRateLimitDenialAudit(ctx, sqlc.ClaimRateLimitDenialAuditParams{
				KeyHash: hash, Now: pgTime(now), AuditBefore: pgTime(now.Add(-denialAuditInterval)),
			})
			if err != nil {
				return mapError(err)
			}
			if claimed > 0 {
				decision.RecordDenialAudit = true
			}
		}
		return nil
	})
	if err != nil {
		return domain.RateLimitDecision{}, err
	}
	return decision, nil
}

// rateLimitKeys returns each distinct key once, sorted by hash, with the longest window of its
// rules. Rules come from domain.NewRateLimitRule; a key used with two scopes is a bug.
func rateLimitKeys(rules []domain.RateLimitRule) ([]rateLimitKey, error) {
	if len(rules) == 0 {
		return nil, domain.NewError(domain.CodeInternal, "rate limit needs at least one rule")
	}
	var keys []rateLimitKey
	for _, rule := range rules {
		i := slices.IndexFunc(keys, func(k rateLimitKey) bool { return bytes.Equal(k.hash, rule.KeyHash) })
		switch {
		case i < 0:
			keys = append(keys, rateLimitKey{hash: rule.KeyHash, scope: rule.Scope, maxWindow: rule.Window})
		case keys[i].scope != rule.Scope:
			return nil, domain.NewError(domain.CodeInternal, "rate limit key is used with two scopes")
		default:
			keys[i].maxWindow = max(keys[i].maxWindow, rule.Window)
		}
	}
	slices.SortFunc(keys, func(a, b rateLimitKey) int { return bytes.Compare(a.hash, b.hash) })
	return keys, nil
}
