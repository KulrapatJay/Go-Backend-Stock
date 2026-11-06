package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-backend-stock/internal/core/service"
)

// The retention tests seed rows in years nobody else's data lives in (rows made by the other tests
// are dated now) and delete what they seeded, so the cutoffs below never meet foreign rows.

const hashSQL = `sha256(gen_random_uuid()::text::bytea)`

type retentionEnv struct {
	admin *pgxpool.Pool
	repo  *RetentionRepository
}

func newRetentionEnv(t *testing.T) retentionEnv {
	t.Helper()
	admin := openPool(t, "TEST_ADMIN_DATABASE_URL")
	return retentionEnv{admin: admin, repo: NewRetentionRepository(openPool(t, "TEST_MAINTENANCE_DATABASE_URL"))}
}

// seed runs an INSERT ... RETURNING on the admin connection, scans the result into dest and
// removes the row again when the test ends.
func (e retentionEnv) seed(t *testing.T, insert, remove string, args []any, dest ...any) {
	t.Helper()
	if err := e.admin.QueryRow(t.Context(), insert, args...).Scan(dest...); err != nil {
		t.Fatalf("seed %.40q: %v", insert, err)
	}
	t.Cleanup(func() { _, _ = e.admin.Exec(context.Background(), remove, dest...) })
}

func (e retentionEnv) exists(t *testing.T, query string, args ...any) bool {
	t.Helper()
	var found bool
	if err := e.admin.QueryRow(t.Context(), "SELECT EXISTS ("+query+")", args...).Scan(&found); err != nil {
		t.Fatalf("exists %.40q: %v", query, err)
	}
	return found
}

func (e retentionEnv) user(t *testing.T) string {
	t.Helper()
	var id string
	e.seed(t, `INSERT INTO app.users (id, email, created_at, updated_at)
		VALUES (gen_random_uuid(), 'retention-' || gen_random_uuid() || '@example.com', now(), now()) RETURNING id::text`,
		`DELETE FROM app.users WHERE id = $1::uuid`, nil, &id)
	return id
}

func (e retentionEnv) portfolio(t *testing.T, userID string) string {
	t.Helper()
	var id string
	e.seed(t, `INSERT INTO app.portfolios (id, user_id, name, currency, created_at, updated_at)
		VALUES (gen_random_uuid(), $1::uuid, 'retention-' || gen_random_uuid(), 'USD', now(), now()) RETURNING id::text`,
		`DELETE FROM app.portfolios WHERE id = $1::uuid`, []any{userID}, &id)
	return id
}

// rateKey stores a rate-limit key that expires at expiresAt and returns its hash. Cleanup removes
// the key's events first, because an event keeps its key.
func (e retentionEnv) rateKey(t *testing.T, expiresAt time.Time) []byte {
	t.Helper()
	var hash []byte
	if err := e.admin.QueryRow(t.Context(), `INSERT INTO app.rate_limit_keys (key_hash, scope, expires_at)
		VALUES (`+hashSQL+`, 'login_ip', $1) RETURNING key_hash`, expiresAt).Scan(&hash); err != nil {
		t.Fatalf("seed rate limit key: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.admin.Exec(context.Background(), `DELETE FROM app.rate_limit_events WHERE key_hash = $1`, hash)
		_, _ = e.admin.Exec(context.Background(), `DELETE FROM app.rate_limit_keys WHERE key_hash = $1`, hash)
	})
	return hash
}

func (e retentionEnv) rateEvent(t *testing.T, hash []byte, at time.Time) int64 {
	t.Helper()
	var id int64
	e.seed(t, `INSERT INTO app.rate_limit_events (key_hash, occurred_at) VALUES ($1, $2) RETURNING id`,
		`DELETE FROM app.rate_limit_events WHERE id = $1`, []any{hash, at}, &id)
	return id
}

// retentionTable seeds one row at the given time and reports whether it is still stored.
type retentionTable struct {
	name   string
	seed   func(t *testing.T, e retentionEnv, at time.Time) (stillThere func() bool)
	delete func(r *RetentionRepository, ctx context.Context, before time.Time, limit int) (int64, error)
}

func retentionTables() []retentionTable {
	return []retentionTable{
		{"audit_events",
			func(t *testing.T, e retentionEnv, at time.Time) func() bool {
				var id string
				e.seed(t, `INSERT INTO app.audit_events (id, occurred_at, event_type, request_id, target_type, outcome)
					VALUES (gen_random_uuid(), $1, 'retention.test', gen_random_uuid(), 'test', 'success') RETURNING id::text`,
					`DELETE FROM app.audit_events WHERE id = $1::uuid`, []any{at}, &id)
				return func() bool { return e.exists(t, `SELECT 1 FROM app.audit_events WHERE id = $1::uuid`, id) }
			},
			(*RetentionRepository).DeleteAuditEventsBefore},
		{"sessions",
			func(t *testing.T, e retentionEnv, at time.Time) func() bool {
				var id string
				e.seed(t, `INSERT INTO app.sessions (id, user_id, token_hash, created_at, expires_at)
					VALUES (gen_random_uuid(), $1::uuid, `+hashSQL+`, $2::timestamptz - interval '1 hour', $2) RETURNING id::text`,
					`DELETE FROM app.sessions WHERE id = $1::uuid`, []any{e.user(t), at}, &id)
				return func() bool { return e.exists(t, `SELECT 1 FROM app.sessions WHERE id = $1::uuid`, id) }
			},
			(*RetentionRepository).DeleteSessionsExpiredBefore},
		{"otp_challenges",
			func(t *testing.T, e retentionEnv, at time.Time) func() bool {
				var id string
				// invalidated_at keeps the rows out of the one-open-challenge-per-user index.
				e.seed(t, `INSERT INTO app.otp_challenges (id, user_id, purpose, otp_hash, created_at, expires_at, invalidated_at)
					VALUES (gen_random_uuid(), $1::uuid, 'password_reset', `+hashSQL+`, $2::timestamptz - interval '1 hour', $2, $2::timestamptz - interval '1 hour')
					RETURNING id::text`,
					`DELETE FROM app.otp_challenges WHERE id = $1::uuid`, []any{e.user(t), at}, &id)
				return func() bool { return e.exists(t, `SELECT 1 FROM app.otp_challenges WHERE id = $1::uuid`, id) }
			},
			(*RetentionRepository).DeleteOTPChallengesExpiredBefore},
		{"oauth_flows",
			func(t *testing.T, e retentionEnv, at time.Time) func() bool {
				var id string
				e.seed(t, `INSERT INTO app.oauth_flows (id, purpose, state_hash, nonce_hash, browser_binding_hash, created_at, expires_at)
					VALUES (gen_random_uuid(), 'login', `+hashSQL+`, `+hashSQL+`, `+hashSQL+`, $1::timestamptz - interval '1 hour', $1) RETURNING id::text`,
					`DELETE FROM app.oauth_flows WHERE id = $1::uuid`, []any{at}, &id)
				return func() bool { return e.exists(t, `SELECT 1 FROM app.oauth_flows WHERE id = $1::uuid`, id) }
			},
			(*RetentionRepository).DeleteOIDCFlowsExpiredBefore},
		{"idempotency_keys",
			func(t *testing.T, e retentionEnv, at time.Time) func() bool {
				user := e.user(t)
				portfolio := e.portfolio(t, user)
				var key string
				e.seed(t, `INSERT INTO app.idempotency_keys (portfolio_id, user_id, operation, key, request_hash, result, created_at, expires_at)
					VALUES ($1::uuid, $2::uuid, 'transactions.create', gen_random_uuid()::text, `+hashSQL+`, '{}', $3::timestamptz - interval '1 hour', $3)
					RETURNING key`,
					`DELETE FROM app.idempotency_keys WHERE key = $1`, []any{portfolio, user, at}, &key)
				return func() bool { return e.exists(t, `SELECT 1 FROM app.idempotency_keys WHERE key = $1`, key) }
			},
			(*RetentionRepository).DeleteIdempotencyKeysExpiredBefore},
		{"rate_limit_events",
			func(t *testing.T, e retentionEnv, at time.Time) func() bool {
				// The key outlives every cutoff used here, so only the event decides.
				id := e.rateEvent(t, e.rateKey(t, time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)), at)
				return func() bool { return e.exists(t, `SELECT 1 FROM app.rate_limit_events WHERE id = $1`, id) }
			},
			(*RetentionRepository).DeleteRateLimitEventsBefore},
	}
}

func TestRetentionDeletesOnlyRowsOlderThanTheCutoff(t *testing.T) {
	cutoff := time.Date(2001, 6, 1, 12, 0, 0, 0, time.UTC)
	for _, table := range retentionTables() {
		t.Run(table.name, func(t *testing.T) {
			e := newRetentionEnv(t)
			older := table.seed(t, e, cutoff.Add(-time.Microsecond))
			exact := table.seed(t, e, cutoff)
			newer := table.seed(t, e, cutoff.Add(time.Microsecond))

			if _, err := table.delete(e.repo, t.Context(), cutoff, 1000); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if older() {
				t.Error("a row 1µs older than the cutoff was kept")
			}
			if !exact() {
				t.Error("a row exactly at the cutoff was deleted; only older rows may go")
			}
			if !newer() {
				t.Error("a row newer than the cutoff was deleted")
			}
		})
	}
}

func TestRetentionBatchRespectsTheLimit(t *testing.T) {
	e := newRetentionEnv(t)
	cutoff := time.Date(1991, 1, 1, 0, 0, 0, 0, time.UTC)
	table := retentionTables()[0]
	var rows []func() bool
	for i := range 5 {
		rows = append(rows, table.seed(t, e, cutoff.Add(-time.Duration(5-i)*time.Hour)))
	}

	n, err := e.repo.DeleteAuditEventsBefore(t.Context(), cutoff, 2)
	if err != nil || n != 2 {
		t.Fatalf("DeleteAuditEventsBefore(limit 2) = %d, %v, want 2 deleted", n, err)
	}
	for i, still := range rows {
		if want := i >= 2; still() != want {
			t.Errorf("row %d (0 is the oldest) stored = %v, want %v: the oldest rows go first", i, still(), want)
		}
	}
}

func TestIdleRateLimitKeysKeepKeysWithEvents(t *testing.T) {
	e := newRetentionEnv(t)
	cutoff := time.Date(2002, 3, 1, 0, 0, 0, 0, time.UTC)
	idle := e.rateKey(t, cutoff.Add(-time.Hour))
	busy := e.rateKey(t, cutoff.Add(-time.Hour))
	e.rateEvent(t, busy, cutoff.Add(-time.Hour))
	live := e.rateKey(t, cutoff.Add(time.Hour))

	if _, err := e.repo.DeleteIdleRateLimitKeys(t.Context(), cutoff, 1000); err != nil {
		t.Fatalf("DeleteIdleRateLimitKeys: %v", err)
	}
	keyStored := func(hash []byte) bool {
		return e.exists(t, `SELECT 1 FROM app.rate_limit_keys WHERE key_hash = $1`, hash)
	}
	if keyStored(idle) {
		t.Error("an expired key without events was kept")
	}
	if !keyStored(busy) {
		t.Error("an expired key that still has an event was deleted")
	}
	if !keyStored(live) {
		t.Error("a key that has not expired was deleted")
	}
}

// TestIdleRateLimitKeyInUseIsSkipped: the API records an event for a key at the very moment the job
// deletes it. The delete waits for that transaction, then the foreign key refuses it; the job must
// treat that as "try again next run", not as a failure.
func TestIdleRateLimitKeyInUseIsSkipped(t *testing.T) {
	e := newRetentionEnv(t)
	cutoff := time.Date(2003, 3, 1, 0, 0, 0, 0, time.UTC)
	key := e.rateKey(t, cutoff.Add(-time.Hour))

	tx, err := e.admin.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = tx.Rollback(context.Background())
		}
	}()
	var eventID int64
	if err := tx.QueryRow(t.Context(),
		`INSERT INTO app.rate_limit_events (key_hash, occurred_at) VALUES ($1, $2) RETURNING id`, key, cutoff.Add(-time.Minute)).Scan(&eventID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.admin.Exec(context.Background(), `DELETE FROM app.rate_limit_events WHERE id = $1`, eventID)
	})

	type result struct {
		n   int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := e.repo.DeleteIdleRateLimitKeys(context.Background(), cutoff, 1000)
		done <- result{n, err}
	}()

	// Wait until the delete is blocked on our row lock, then let the event commit.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := e.admin.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity WHERE usename = 'app_maintenance' AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the delete never waited for the lock of the key in use")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit event: %v", err)
	}
	finished = true

	select {
	case got := <-done:
		if got.err != nil || got.n != 0 {
			t.Fatalf("DeleteIdleRateLimitKeys = %d, %v, want 0 deleted and no error", got.n, got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DeleteIdleRateLimitKeys did not return after the event committed")
	}
	if !e.exists(t, `SELECT 1 FROM app.rate_limit_keys WHERE key_hash = $1`, key) ||
		!e.exists(t, `SELECT 1 FROM app.rate_limit_events WHERE id = $1`, eventID) {
		t.Error("the key in use or its event is gone")
	}
}

func TestRuntimeRoleCannotRunRetention(t *testing.T) {
	repo := NewRetentionRepository(openPool(t, "TEST_RUNTIME_DATABASE_URL"))
	_, err := repo.DeleteAuditEventsBefore(t.Context(), time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC), 10)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("DeleteAuditEventsBefore as app_runtime = %v, want SQLSTATE 42501 insufficient_privilege", err)
	}
}

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// TestRetentionNeverTouchesBusinessTables seeds business rows dated 2004 (rows other tests create are
// dated now, so they can neither be deleted by this cutoff nor change these counts while the HTTP
// tests run in parallel against the same database) and checks that exactly those rows survive a run
// that deletes the expired session and idempotency record around them.
func TestRetentionNeverTouchesBusinessTables(t *testing.T) {
	e := newRetentionEnv(t)
	old := time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2004, 6, 1, 3, 0, 0, 0, time.UTC)

	var user, portfolio string
	e.seed(t, `INSERT INTO app.users (id, email, created_at, updated_at)
		VALUES (gen_random_uuid(), 'retention-' || gen_random_uuid() || '@example.com', $1, $1) RETURNING id::text`,
		`DELETE FROM app.users WHERE id = $1::uuid`, []any{old}, &user)
	e.seed(t, `INSERT INTO app.portfolios (id, user_id, name, currency, created_at, updated_at)
		VALUES (gen_random_uuid(), $1::uuid, 'retention-' || gen_random_uuid(), 'USD', $2, $2) RETURNING id::text`,
		`DELETE FROM app.portfolios WHERE id = $1::uuid`, []any{user, old}, &portfolio)
	var session, key string
	e.seed(t, `INSERT INTO app.sessions (id, user_id, token_hash, created_at, expires_at)
		VALUES (gen_random_uuid(), $1::uuid, `+hashSQL+`, $2, $3) RETURNING id::text`,
		`DELETE FROM app.sessions WHERE id = $1::uuid`, []any{user, old, now.Add(-48 * time.Hour)}, &session)
	e.seed(t, `INSERT INTO app.idempotency_keys (portfolio_id, user_id, operation, key, request_hash, result, created_at, expires_at)
		VALUES ($1::uuid, $2::uuid, 'transactions.create', gen_random_uuid()::text, `+hashSQL+`, '{}', $3, $4) RETURNING key`,
		`DELETE FROM app.idempotency_keys WHERE key = $1`, []any{portfolio, user, old, now.Add(-time.Hour)}, &key)

	retention := service.NewRetention(e.repo, fixedClock{now}, service.RetentionPolicy{AuditRetention: 90 * 24 * time.Hour, BatchSize: 1000})
	report, err := retention.Run(t.Context())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Sessions < 1 || report.IdempotencyKeys < 1 {
		t.Errorf("report = %+v, want the expired session and idempotency record deleted", report)
	}
	if e.exists(t, `SELECT 1 FROM app.sessions WHERE id = $1::uuid`, session) ||
		e.exists(t, `SELECT 1 FROM app.idempotency_keys WHERE key = $1`, key) {
		t.Error("an expired row was kept")
	}
	if !e.exists(t, `SELECT 1 FROM app.users WHERE id = $1::uuid`, user) ||
		!e.exists(t, `SELECT 1 FROM app.portfolios WHERE id = $1::uuid`, portfolio) {
		t.Error("retention deleted a user or portfolio")
	}
	for _, table := range []string{"users", "portfolios", "auth_providers", "transactions"} {
		var n int
		if err := e.admin.QueryRow(t.Context(), "SELECT count(*) FROM app."+table+" WHERE created_at < $1", old.AddDate(1, 0, 0)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		want := 0
		if table == "users" || table == "portfolios" {
			want = 1 // the rows seeded above
		}
		if n != want {
			t.Errorf("app.%s has %d rows dated before 2005, want %d", table, n, want)
		}
	}
}
