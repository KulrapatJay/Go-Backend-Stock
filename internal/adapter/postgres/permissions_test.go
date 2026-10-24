package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// Each statement is planned inside a transaction that is rolled back. PostgreSQL checks
// privileges when planning, so "WHERE false" statements prove the grant without touching rows.
type privilegeCase struct {
	sql     string
	allowed bool
}

func checkPrivileges(t *testing.T, urlKey string, cases []privilegeCase) {
	t.Helper()
	pool := openPool(t, urlKey)
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()

			_, err = tx.Exec(t.Context(), c.sql)
			var pgErr *pgconn.PgError
			denied := errors.As(err, &pgErr) && pgErr.Code == insufficientPrivilege
			switch {
			case c.allowed && err != nil:
				t.Fatalf("want allowed, got %v", err)
			case !c.allowed && !denied:
				t.Fatalf("want SQLSTATE %s, got %v", insufficientPrivilege, err)
			}
		})
	}
}

func TestRuntimeRolePrivileges(t *testing.T) {
	checkPrivileges(t, "TEST_RUNTIME_DATABASE_URL", []privilegeCase{
		// audit_events is append-only and unreadable for the runtime role.
		{`INSERT INTO app.audit_events SELECT * FROM app.audit_events WHERE false`, false}, // needs SELECT too
		{`INSERT INTO app.audit_events (id, occurred_at, event_type, request_id, target_type, outcome)
		  SELECT gen_random_uuid(), now(), 'x', gen_random_uuid(), 't', 'success' WHERE false`, true},
		{`SELECT 1 FROM app.audit_events LIMIT 1`, false},
		{`UPDATE app.audit_events SET outcome = 'success' WHERE false`, false},
		{`DELETE FROM app.audit_events WHERE false`, false},
		// No deletes of accounts, sessions, challenges, flows or instruments.
		{`DELETE FROM app.users WHERE false`, false},
		{`DELETE FROM app.sessions WHERE false`, false},
		{`DELETE FROM app.otp_challenges WHERE false`, false},
		{`DELETE FROM app.oauth_flows WHERE false`, false},
		{`UPDATE app.instruments SET name = name WHERE false`, false},
		{`DELETE FROM app.instruments WHERE false`, false},
		{`UPDATE app.auth_providers SET provider = provider WHERE false`, false},
		{`DELETE FROM app.rate_limit_events WHERE false`, false},
		// What the repositories need.
		{`SELECT 1 FROM app.users LIMIT 1`, true},
		{`UPDATE app.users SET updated_at = updated_at WHERE false`, true},
		{`UPDATE app.sessions SET revoked_at = revoked_at WHERE false`, true},
		{`DELETE FROM app.portfolios WHERE false`, true},
		{`DELETE FROM app.transactions WHERE false`, true},
		{`DELETE FROM app.idempotency_keys WHERE false`, true},
		{`UPDATE app.rate_limit_keys SET last_denial_audit_at = now() WHERE false`, true},
		// No schema changes.
		{`CREATE TABLE app.should_not_exist (id int)`, false},
		{`ALTER TABLE app.users ADD COLUMN x int`, false},
	})
}

func TestMaintenanceRolePrivileges(t *testing.T) {
	checkPrivileges(t, "TEST_MAINTENANCE_DATABASE_URL", []privilegeCase{
		// Cleanup: read the expiry columns and delete expired rows.
		{`DELETE FROM app.audit_events WHERE occurred_at < now() - interval '90 days' AND false`, true},
		{`SELECT id, occurred_at FROM app.audit_events LIMIT 1`, true},
		{`DELETE FROM app.sessions WHERE expires_at < now() AND false`, true},
		{`DELETE FROM app.otp_challenges WHERE expires_at < now() AND false`, true},
		{`DELETE FROM app.oauth_flows WHERE expires_at < now() AND false`, true},
		{`DELETE FROM app.idempotency_keys WHERE expires_at < now() AND false`, true},
		{`DELETE FROM app.rate_limit_events WHERE occurred_at < now() AND false`, true},
		{`DELETE FROM app.rate_limit_keys WHERE expires_at < now() AND false`, true},
		// Nothing else: no reading payloads or secrets, no writes, no business tables.
		{`SELECT metadata FROM app.audit_events LIMIT 1`, false},
		{`SELECT token_hash FROM app.sessions LIMIT 1`, false},
		{`SELECT result FROM app.idempotency_keys LIMIT 1`, false},
		{`INSERT INTO app.audit_events (id, occurred_at, event_type, request_id, target_type, outcome)
		  SELECT gen_random_uuid(), now(), 'x', gen_random_uuid(), 't', 'success' WHERE false`, false},
		{`UPDATE app.audit_events SET outcome = 'success' WHERE false`, false},
		{`SELECT 1 FROM app.users LIMIT 1`, false},
		{`SELECT 1 FROM app.portfolios LIMIT 1`, false},
		{`DELETE FROM app.transactions WHERE false`, false},
		{`CREATE TABLE app.should_not_exist (id int)`, false},
	})
}

// TestRuntimeCanInsertIdentityColumns proves the runtime role can insert rows whose id comes
// from an identity sequence (rate_limit_events, and transactions.sequence in milestone 3c).
func TestRuntimeCanInsertIdentityColumns(t *testing.T) {
	pool := openPool(t, "TEST_RUNTIME_DATABASE_URL")
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(),
		`INSERT INTO app.rate_limit_keys (key_hash, scope, expires_at) VALUES ($1, 'login_ip', now() + interval '1 hour')`,
		make([]byte, 32)); err != nil {
		t.Fatalf("insert key: %v", err)
	}
	var id int64
	if err := tx.QueryRow(t.Context(),
		`INSERT INTO app.rate_limit_events (key_hash, occurred_at) VALUES ($1, now()) RETURNING id`,
		make([]byte, 32)).Scan(&id); err != nil || id <= 0 {
		t.Fatalf("insert event as runtime: id=%d err=%v", id, err)
	}
}
