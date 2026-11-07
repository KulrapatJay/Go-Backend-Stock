package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type retentionCall struct {
	method string
	before time.Time
	limit  int
}

// fakeRetentionRepo answers each method from a script of counts (0 once the script runs out) and
// records every call in order.
type fakeRetentionRepo struct {
	calls  []retentionCall
	script map[string][]int64
	errs   map[string]error
	after  func(method string) // runs after each call, before it returns
}

func (f *fakeRetentionRepo) do(method string, before time.Time, limit int) (int64, error) {
	f.calls = append(f.calls, retentionCall{method, before, limit})
	if f.after != nil {
		defer f.after(method)
	}
	if err := f.errs[method]; err != nil {
		return 0, err
	}
	queue := f.script[method]
	if len(queue) == 0 {
		return 0, nil
	}
	f.script[method] = queue[1:]
	return queue[0], nil
}

func (f *fakeRetentionRepo) DeleteAuditEventsBefore(_ context.Context, before time.Time, limit int) (int64, error) {
	return f.do("audit_events", before, limit)
}

func (f *fakeRetentionRepo) DeleteIdempotencyKeysExpiredBefore(_ context.Context, before time.Time, limit int) (int64, error) {
	return f.do("idempotency_keys", before, limit)
}

func (f *fakeRetentionRepo) DeleteSessionsExpiredBefore(_ context.Context, before time.Time, limit int) (int64, error) {
	return f.do("sessions", before, limit)
}

func (f *fakeRetentionRepo) DeleteOTPChallengesExpiredBefore(_ context.Context, before time.Time, limit int) (int64, error) {
	return f.do("otp_challenges", before, limit)
}

func (f *fakeRetentionRepo) DeleteOIDCFlowsExpiredBefore(_ context.Context, before time.Time, limit int) (int64, error) {
	return f.do("oauth_flows", before, limit)
}

func (f *fakeRetentionRepo) DeleteRateLimitEventsBefore(_ context.Context, before time.Time, limit int) (int64, error) {
	return f.do("rate_limit_events", before, limit)
}

func (f *fakeRetentionRepo) DeleteIdleRateLimitKeys(_ context.Context, before time.Time, limit int) (int64, error) {
	return f.do("rate_limit_keys", before, limit)
}

func (f *fakeRetentionRepo) methods() []string {
	names := make([]string, len(f.calls))
	for i, c := range f.calls {
		names[i] = c.method
	}
	return names
}

func newRetention(t *testing.T, repo *fakeRetentionRepo) *Retention {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, time.November, 6, 3, 0, 0, 0, time.UTC)}
	return NewRetention(repo, clock, RetentionPolicy{AuditRetention: 90 * 24 * time.Hour, BatchSize: 1000})
}

func TestRetentionUsesTheCutoffOfEachTable(t *testing.T) {
	repo := &fakeRetentionRepo{}
	if _, err := newRetention(t, repo).Run(t.Context()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	utc := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	want := map[string]time.Time{
		"audit_events":      utc("2026-08-08T03:00:00Z"),
		"idempotency_keys":  utc("2026-11-06T03:00:00Z"),
		"sessions":          utc("2026-11-05T03:00:00Z"),
		"otp_challenges":    utc("2026-11-05T03:00:00Z"),
		"oauth_flows":       utc("2026-11-05T03:00:00Z"),
		"rate_limit_events": utc("2026-11-06T02:00:00Z"),
		"rate_limit_keys":   utc("2026-11-06T03:00:00Z"),
	}
	if len(repo.calls) != len(want) {
		t.Fatalf("calls = %v, want one per table", repo.methods())
	}
	for _, c := range repo.calls {
		if !c.before.Equal(want[c.method]) {
			t.Errorf("%s cutoff = %s, want %s", c.method, c.before, want[c.method])
		}
		if c.limit != 1000 {
			t.Errorf("%s limit = %d, want 1000", c.method, c.limit)
		}
	}
}

func TestRetentionRepeatsUntilABatchIsShort(t *testing.T) {
	repo := &fakeRetentionRepo{script: map[string][]int64{"sessions": {1000, 1000, 3}}}
	report, err := newRetention(t, repo).Run(t.Context())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	sessionCalls := 0
	for _, m := range repo.methods() {
		if m == "sessions" {
			sessionCalls++
		}
	}
	if sessionCalls != 3 || report.Sessions != 2003 {
		t.Errorf("sessions called %d times, report.Sessions = %d, want 3 and 2003", sessionCalls, report.Sessions)
	}
	if len(repo.calls) != 3+6 {
		t.Errorf("calls = %v, want a single call for every table that returned 0", repo.methods())
	}
}

func TestRetentionDeletesRateEventsBeforeKeys(t *testing.T) {
	repo := &fakeRetentionRepo{}
	if _, err := newRetention(t, repo).Run(t.Context()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := []string{"audit_events", "idempotency_keys", "sessions", "otp_challenges", "oauth_flows", "rate_limit_events", "rate_limit_keys"}
	if got := repo.methods(); !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestRetentionStopsAtTheFirstError(t *testing.T) {
	boom := errors.New("connection reset")
	repo := &fakeRetentionRepo{
		script: map[string][]int64{"audit_events": {7}, "idempotency_keys": {4}, "sessions": {2}},
		errs:   map[string]error{"otp_challenges": boom},
	}
	report, err := newRetention(t, repo).Run(t.Context())
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "otp_challenges") {
		t.Fatalf("Run() error = %v, want it to wrap the cause and name otp_challenges", err)
	}
	if report.AuditEvents != 7 || report.IdempotencyKeys != 4 || report.Sessions != 2 {
		t.Errorf("report = %+v, want what was deleted before the failure", report)
	}
	for _, m := range repo.methods() {
		if m == "oauth_flows" || strings.HasPrefix(m, "rate_limit") {
			t.Errorf("%s ran after the failure", m)
		}
	}
}

func TestRetentionStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	repo := &fakeRetentionRepo{script: map[string][]int64{"audit_events": {1000, 1000}}}
	repo.after = func(string) { cancel() }

	_, err := newRetention(t, repo).Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if len(repo.calls) != 1 {
		t.Errorf("calls = %v, want the run to stop after the first batch", repo.methods())
	}
}

func TestRetentionRejectsABatchSizeThatCouldNeverFinish(t *testing.T) {
	for _, size := range []int{0, -1} {
		repo := &fakeRetentionRepo{}
		r := NewRetention(repo, newFakeClock(), RetentionPolicy{AuditRetention: 90 * 24 * time.Hour, BatchSize: size})
		if _, err := r.Run(t.Context()); err == nil {
			t.Errorf("BatchSize %d: Run() succeeded", size)
		}
		if len(repo.calls) != 0 {
			t.Errorf("BatchSize %d: deleted with %v", size, repo.methods())
		}
	}
}
