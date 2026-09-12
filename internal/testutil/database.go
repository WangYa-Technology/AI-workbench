package testutil

import (
	"os"
	"testing"
)

// DatabaseUnavailable keeps local unit runs usable while making explicitly
// configured integration runs fail closed instead of reporting false green.
func DatabaseUnavailable(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") != "" || os.Getenv("HCAI_REQUIRE_INTEGRATION_TESTS") == "1" {
		t.Fatalf("PostgreSQL integration database unavailable: %v", err)
	}
	t.Skipf("PostgreSQL integration database unavailable: %v", err)
}

func ExternalDatabaseUnavailable(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") != "" || os.Getenv("HCAI_REQUIRE_INTEGRATION_TESTS") == "1" {
		t.Fatalf("PostgreSQL unavailable: %v", err)
	}
	t.Skipf("PostgreSQL unavailable: %v", err)
}
