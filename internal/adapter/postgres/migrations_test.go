package postgres

import "testing"

// TestMigrationsCreateEveryTable checks that migrations 00002-00004 created every phase 1 table in
// schema app (and nothing else there besides goose's version table).
func TestMigrationsCreateEveryTable(t *testing.T) {
	want := []string{
		"audit_events", "auth_providers", "goose_db_version", "idempotency_keys", "instruments", "oauth_flows",
		"otp_challenges", "portfolios", "rate_limit_events", "rate_limit_keys", "sessions", "transactions", "users",
	}
	rows, err := openPool(t, "TEST_ADMIN_DATABASE_URL").Query(t.Context(),
		`SELECT tablename FROM pg_tables WHERE schemaname = 'app' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if len(got) != len(want) {
		t.Fatalf("tables in app = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tables in app = %v, want %v", got, want)
		}
	}
}
