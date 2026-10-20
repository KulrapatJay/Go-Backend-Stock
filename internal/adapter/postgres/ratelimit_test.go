package postgres

import (
	"errors"
	"strings"
	"sync/atomic"
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

// hashStartingWith returns a random key hash whose first byte is b, so a test can decide which
// key sorts first by hash.
func hashStartingWith(t *testing.T, b byte) []byte {
	t.Helper()
	h := randomHash(t)
	h[0] = b
	return h
}

func adminCount(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := openPool(t, "TEST_ADMIN_DATABASE_URL").QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestRateLimitDeniedClientKeyCreatesNoLaterKeys: once the IP is over its quota, a request for a
// new email must not store a key row for that email (phase 1 spec section 10: limit storage
// abuse). The email hash sorts first, so hash-only ordering would upsert it before the IP.
func TestRateLimitDeniedClientKeyCreatesNoLaterKeys(t *testing.T) {
	store := newRuntimeStore(t)
	ip := rule(t, domain.RateLimitLoginIP, hashStartingWith(t, 0xFF), 1, time.Hour)
	email := rule(t, domain.RateLimitLoginEmail, hashStartingWith(t, 0x00), 10, time.Hour)
	start := now()
	admit(t, store, start, ip)

	if d := admit(t, store, start.Add(time.Second), email, ip); d.Allowed {
		t.Fatal("request allowed although the IP key is full")
	}

	if n := adminCount(t, `SELECT count(*) FROM app.rate_limit_keys WHERE key_hash = $1`, email.KeyHash); n != 0 {
		t.Errorf("%d key rows created for the email after the IP was denied, want 0", n)
	}
}

func TestScopeRankPutsClientIPScopesFirst(t *testing.T) {
	for _, s := range domain.RateLimitScopes() {
		want := 1
		if strings.HasSuffix(string(s), "_ip") {
			want = 0
		}
		if got := scopeRank(s); got != want {
			t.Errorf("scopeRank(%s) = %d, want %d", s, got, want)
		}
	}
	if scopeRank(domain.RateLimitLoginEmail) != 1 || scopeRank(domain.RateLimitLoginIP) != 0 || scopeRank(domain.RateLimitInstrumentLookupUser) != 1 {
		t.Error("login_email and instrument_lookup_user must rank after login_ip")
	}
}

// TestRateLimitConcurrentMultiKeyAdmissionsDoNotDeadlock: requests that share two keys but list
// them in opposite orders must lock in the same order, or PostgreSQL aborts one with 40P01.
func TestRateLimitConcurrentMultiKeyAdmissionsDoNotDeadlock(t *testing.T) {
	store := newRuntimeStore(t)
	a := rule(t, domain.RateLimitLoginIP, hashStartingWith(t, 0x10), 1000, time.Hour)
	b := rule(t, domain.RateLimitSignupIP, hashStartingWith(t, 0x20), 1000, time.Hour)
	var turn atomic.Int32

	succeeded, errs := runConcurrently(40, func() error {
		rules := []domain.RateLimitRule{a, b}
		if turn.Add(1)%2 == 0 {
			rules = []domain.RateLimitRule{b, a}
		}
		_, err := store.RateLimits().Admit(t.Context(), rules, now())
		return err
	})

	if succeeded != 40 {
		t.Fatalf("%d of 40 concurrent multi-key admissions succeeded; errors: %v", succeeded, errs)
	}
}

// TestRateLimitRetryAfterIsTheLongestDenyingRule: with the hourly and cooldown rules of one key
// both denying, the client must wait for the slower one.
func TestRateLimitRetryAfterIsTheLongestDenyingRule(t *testing.T) {
	store := newRuntimeStore(t)
	key := randomHash(t)
	hourly := rule(t, domain.RateLimitPasswordResetRequestEmail, key, 1, time.Hour)
	cooldown := rule(t, domain.RateLimitPasswordResetRequestEmail, key, 1, time.Minute)
	start := now()
	admit(t, store, start, hourly, cooldown)

	d := admit(t, store, start.Add(10*time.Second), hourly, cooldown)

	if d.Allowed || d.RetryAfter != 59*time.Minute+50*time.Second {
		t.Fatalf("decision = %+v, want denied with RetryAfter 59m50s (the hourly rule)", d)
	}
}

// TestRateLimitDeniedRequestRecordsNoEventsForAllowedKeys: a denied request leaves no trace on the
// keys that would have allowed it.
func TestRateLimitDeniedRequestRecordsNoEventsForAllowedKeys(t *testing.T) {
	store := newRuntimeStore(t)
	full := rule(t, domain.RateLimitLoginIP, hashStartingWith(t, 0x00), 1, time.Hour)
	open := rule(t, domain.RateLimitLoginEmail, hashStartingWith(t, 0xFF), 10, time.Hour)
	start := now()
	admit(t, store, start, full)

	if d := admit(t, store, start.Add(time.Second), full, open); d.Allowed {
		t.Fatal("request allowed although one key is full")
	}

	if n := adminCount(t, `SELECT count(*) FROM app.rate_limit_events WHERE key_hash = $1`, open.KeyHash); n != 0 {
		t.Errorf("%d events recorded for the allowed key of a denied request, want 0", n)
	}
}
