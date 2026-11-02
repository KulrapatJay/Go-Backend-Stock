package postgres

import (
	"strings"
	"testing"
)

func TestNewPoolErrorDoesNotLeakPassword(t *testing.T) {
	_, err := NewPool(t.Context(), "postgres://app_runtime:S3cretPassw0rd@localhost:5432/stock?sslmode=bogus", 2)
	if err == nil {
		t.Fatal("NewPool() error = nil, want invalid sslmode error")
	}
	if strings.Contains(err.Error(), "S3cretPassw0rd") {
		t.Fatalf("error leaked the password: %v", err)
	}
}

func TestNewPoolPingsDatabase(t *testing.T) {
	pool, err := NewPool(t.Context(), integrationURL(t, "TEST_RUNTIME_DATABASE_URL"), 2)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestNewPoolSetsLockAndStatementTimeouts(t *testing.T) {
	pool, err := NewPool(t.Context(), integrationURL(t, "TEST_RUNTIME_DATABASE_URL"), 2)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()

	for setting, want := range map[string]string{"lock_timeout": "3s", "statement_timeout": "5s"} {
		var got string
		if err := pool.QueryRow(t.Context(), "SHOW "+setting).Scan(&got); err != nil {
			t.Fatalf("SHOW %s: %v", setting, err)
		}
		if got != want {
			t.Errorf("%s = %q, want %q", setting, got, want)
		}
	}
}
