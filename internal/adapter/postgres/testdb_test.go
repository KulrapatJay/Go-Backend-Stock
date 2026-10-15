package postgres

import (
	"os"
	"testing"
)

// integrationURL returns a database URL from the environment. Integration tests are skipped
// locally when it is unset, but fail in CI where REQUIRE_INTEGRATION=1.
func integrationURL(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		if os.Getenv("REQUIRE_INTEGRATION") == "1" {
			t.Fatalf("%s is required when REQUIRE_INTEGRATION=1", key)
		}
		t.Skipf("%s not set; skipping integration test", key)
	}
	return v
}
