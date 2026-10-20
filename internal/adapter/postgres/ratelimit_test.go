package postgres

import (
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func rule(t *testing.T, scope domain.RateLimitScope, key []byte, limit int, window time.Duration) domain.RateLimitRule {
	t.Helper()
	r, err := domain.NewRateLimitRule(domain.RateLimitRule{Scope: scope, KeyHash: key, Limit: limit, Window: window})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func admit(t *testing.T, store *Store, at time.Time, rules ...domain.RateLimitRule) domain.RateLimitDecision {
	t.Helper()
	d, err := store.RateLimits().Admit(t.Context(), rules, at)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	return d
}

func TestRateLimitAllowsUpToLimitThenDenies(t *testing.T) {
	store := newRuntimeStore(t)
	r := rule(t, domain.RateLimitLoginIP, randomHash(t), 3, time.Hour)
	start := now()

	for i := range 3 {
		if d := admit(t, store, start.Add(time.Duration(i)*time.Minute), r); !d.Allowed {
			t.Fatalf("attempt %d denied, want allowed", i+1)
		}
	}
	at := start.Add(10 * time.Minute)
	d := admit(t, store, at, r)
	if d.Allowed {
		t.Fatal("4th attempt within the hour allowed, want denied")
	}
	// The oldest attempt (at start) leaves the window at start+1h.
	if want := start.Add(time.Hour).Sub(at); d.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v", d.RetryAfter, want)
	}
	// Once the oldest attempt leaves the window, one more attempt fits.
	if d := admit(t, store, start.Add(time.Hour), r); !d.Allowed {
		t.Error("attempt after the oldest left the window denied, want allowed")
	}
}

// TestRateLimitDeniedAttemptsAreNotCounted: an attacker hammering a denied key does not push the
// window forward, and a user who waits is let back in on time.
func TestRateLimitDeniedAttemptsAreNotCounted(t *testing.T) {
	store := newRuntimeStore(t)
	r := rule(t, domain.RateLimitLoginEmail, randomHash(t), 1, time.Minute)
	start := now()

	admit(t, store, start, r)
	for i := 1; i <= 5; i++ {
		if admit(t, store, start.Add(time.Duration(i)*time.Second), r).Allowed {
			t.Fatal("denied key allowed within the window")
		}
	}
	if d := admit(t, store, start.Add(time.Minute), r); !d.Allowed {
		t.Fatalf("attempt after the window denied (RetryAfter %v): denied attempts were counted", d.RetryAfter)
	}
}

// TestRateLimitCooldownAndHourlyQuotaShareOneKey: *_request_email has a 60 s cooldown and 5 per
// hour on the same key (phase 1 spec section 7).
func TestRateLimitCooldownAndHourlyQuotaShareOneKey(t *testing.T) {
	store := newRuntimeStore(t)
	key := randomHash(t)
	cooldown := rule(t, domain.RateLimitPasswordResetRequestEmail, key, 1, time.Minute)
	hourly := rule(t, domain.RateLimitPasswordResetRequestEmail, key, 5, time.Hour)
	start := now()

	if !admit(t, store, start, cooldown, hourly).Allowed {
		t.Fatal("first request denied")
	}
	d := admit(t, store, start.Add(10*time.Second), cooldown, hourly)
	if d.Allowed || d.RetryAfter != 50*time.Second {
		t.Fatalf("request inside the cooldown = %+v, want denied with RetryAfter 50s", d)
	}
	for i := 1; i <= 4; i++ {
		if !admit(t, store, start.Add(time.Duration(i)*time.Minute), cooldown, hourly).Allowed {
			t.Fatalf("request %d after each cooldown denied", i+1)
		}
	}
	d = admit(t, store, start.Add(5*time.Minute), cooldown, hourly)
	if d.Allowed || d.RetryAfter != 55*time.Minute {
		t.Fatalf("6th request in the hour = %+v, want denied until the first leaves the hour (55m)", d)
	}
}

func TestRateLimitRecordsDenialAuditOncePerMinute(t *testing.T) {
	store := newRuntimeStore(t)
	r := rule(t, domain.RateLimitSignupIP, randomHash(t), 1, time.Hour)
	start := now()
	admit(t, store, start, r)

	if d := admit(t, store, start.Add(time.Second), r); d.Allowed || !d.RecordDenialAudit {
		t.Fatalf("first denial = %+v, want RecordDenialAudit", d)
	}
	if d := admit(t, store, start.Add(30*time.Second), r); d.Allowed || d.RecordDenialAudit {
		t.Fatalf("second denial within a minute = %+v, want no audit", d)
	}
	if d := admit(t, store, start.Add(time.Second+time.Minute), r); d.Allowed || !d.RecordDenialAudit {
		t.Fatalf("denial a minute later = %+v, want RecordDenialAudit again", d)
	}
}

func TestRateLimitKeysAreIndependent(t *testing.T) {
	store := newRuntimeStore(t)
	a := rule(t, domain.RateLimitLoginIP, randomHash(t), 1, time.Hour)
	b := rule(t, domain.RateLimitLoginIP, randomHash(t), 1, time.Hour)
	start := now()
	admit(t, store, start, a)

	if !admit(t, store, start, b).Allowed {
		t.Fatal("a full key blocked a different key")
	}
	// A request checked against both keys is denied when either is full, and records nothing.
	if admit(t, store, start.Add(time.Second), a, rule(t, domain.RateLimitLoginEmail, randomHash(t), 5, time.Hour)).Allowed {
		t.Fatal("request allowed although one of its keys is full")
	}
}

// TestRateLimitConcurrentAdmissionsRespectTheLimit: 20 simultaneous requests against a limit of 5
// admit exactly 5, because each admission locks the key row.
func TestRateLimitConcurrentAdmissionsRespectTheLimit(t *testing.T) {
	store := newRuntimeStore(t)
	r := rule(t, domain.RateLimitPasswordResetConfirmIP, randomHash(t), 5, time.Hour)
	at := now()

	allowed, errs := runConcurrently(20, func() error {
		d, err := store.RateLimits().Admit(t.Context(), []domain.RateLimitRule{r}, at)
		if err != nil {
			return err
		}
		if !d.Allowed {
			return errors.New("denied")
		}
		return nil
	})

	if allowed != 5 {
		t.Fatalf("%d of 20 concurrent requests allowed, want exactly 5", allowed)
	}
	for _, err := range errs {
		if err.Error() != "denied" {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestRateLimitRejectsInvalidRuleSets(t *testing.T) {
	store := newRuntimeStore(t)
	if _, err := store.RateLimits().Admit(t.Context(), nil, now()); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("Admit(no rules) error = %v, want internal", err)
	}
	key := randomHash(t)
	twoScopes := []domain.RateLimitRule{
		rule(t, domain.RateLimitLoginIP, key, 5, time.Hour),
		rule(t, domain.RateLimitSignupIP, key, 5, time.Hour),
	}
	if _, err := store.RateLimits().Admit(t.Context(), twoScopes, now()); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("Admit(one key, two scopes) error = %v, want internal", err)
	}
}
