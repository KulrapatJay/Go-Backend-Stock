package postgres

import (
	"bytes"
	"context"
	"slices"
	"strings"
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
	rules     []domain.RateLimitRule
}

// Admit checks every rule in one transaction. Keys are visited in lock order (client-IP keys
// first, then by hash); each key is upserted, which locks its row, and then checked. The first key
// that denies ends the check: nothing is recorded, no later key is created (so an IP over its
// quota cannot grow storage with arbitrary emails), RetryAfter is the longest wait among that
// key's denying rules, and RecordDenialAudit is set when this is the key's first denial in a
// minute. When every key allows, one event per key is recorded (admission limit: denied attempts
// do not extend the wait). now comes from the service's Clock, never from the database.
func (r *RateLimitRepository) Admit(ctx context.Context, rules []domain.RateLimitRule, now time.Time) (domain.RateLimitDecision, error) {
	keys, err := rateLimitKeys(rules)
	if err != nil {
		return domain.RateLimitDecision{}, err
	}
	var decision domain.RateLimitDecision
	err = r.store.withinTx(ctx, func(tx *txRepositories) error {
		for _, k := range keys {
			if err := tx.q.UpsertRateLimitKey(ctx, sqlc.UpsertRateLimitKeyParams{
				KeyHash: k.hash, Scope: string(k.scope), ExpiresAt: pgTime(now.Add(k.maxWindow)),
			}); err != nil {
				return mapError(err)
			}

			var retryAfter time.Duration
			denied := false
			for _, rule := range k.rules {
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
				denied = true
			}
			if !denied {
				continue
			}

			claimed, err := tx.q.ClaimRateLimitDenialAudit(ctx, sqlc.ClaimRateLimitDenialAuditParams{
				KeyHash: k.hash, Now: pgTime(now), AuditBefore: pgTime(now.Add(-denialAuditInterval)),
			})
			if err != nil {
				return mapError(err)
			}
			decision = domain.RateLimitDecision{RetryAfter: retryAfter, RecordDenialAudit: claimed > 0, Scope: k.scope}
			return nil
		}

		for _, k := range keys {
			if err := tx.q.InsertRateLimitEvent(ctx, sqlc.InsertRateLimitEventParams{KeyHash: k.hash, OccurredAt: pgTime(now)}); err != nil {
				return mapError(err)
			}
		}
		decision = domain.RateLimitDecision{Allowed: true}
		return nil
	})
	if err != nil {
		return domain.RateLimitDecision{}, err
	}
	return decision, nil
}

// rateLimitKeys returns each distinct key once with its rules and the longest window among them,
// in lock order: client-IP keys first, then by hash. A key's scope is part of its hash, so the
// order is the same for every request. Rules come from domain.NewRateLimitRule; a key used with
// two scopes is a bug.
func rateLimitKeys(rules []domain.RateLimitRule) ([]rateLimitKey, error) {
	if len(rules) == 0 {
		return nil, domain.NewError(domain.CodeInternal, "rate limit needs at least one rule")
	}
	var keys []rateLimitKey
	for _, rule := range rules {
		i := slices.IndexFunc(keys, func(k rateLimitKey) bool { return bytes.Equal(k.hash, rule.KeyHash) })
		switch {
		case i < 0:
			keys = append(keys, rateLimitKey{hash: rule.KeyHash, scope: rule.Scope, maxWindow: rule.Window, rules: []domain.RateLimitRule{rule}})
		case keys[i].scope != rule.Scope:
			return nil, domain.NewError(domain.CodeInternal, "rate limit key is used with two scopes")
		default:
			keys[i].maxWindow = max(keys[i].maxWindow, rule.Window)
			keys[i].rules = append(keys[i].rules, rule)
		}
	}
	slices.SortFunc(keys, func(a, b rateLimitKey) int {
		if c := scopeRank(a.scope) - scopeRank(b.scope); c != 0 {
			return c
		}
		return bytes.Compare(a.hash, b.hash)
	})
	return keys, nil
}

// scopeRank orders lock acquisition: client-IP keys (rank 0) come before every other key (rank 1),
// so a request over its IP quota is denied before any email or user key is created.
func scopeRank(scope domain.RateLimitScope) int {
	if strings.HasSuffix(string(scope), "_ip") {
		return 0
	}
	return 1
}
