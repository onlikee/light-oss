package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestRateLimitMigrationStoresHashedExpiringBuckets(t *testing.T) {
	upSQL, err := os.ReadFile("000008_rate_limit_buckets.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}

	upText := string(upSQL)
	for _, snippet := range []string{
		"CREATE TABLE rate_limit_buckets",
		"key_hash BINARY(32) NOT NULL PRIMARY KEY",
		"tokens DOUBLE NOT NULL",
		"expires_at DATETIME(6) NOT NULL",
		"last_allowed BOOLEAN NOT NULL",
		"idx_rate_limit_buckets_expires_at",
		"CREATE TABLE rate_limit_capacity",
		"entry_count BIGINT UNSIGNED NOT NULL DEFAULT 0",
		"capacity_rejections BIGINT UNSIGNED NOT NULL DEFAULT 0",
		"INSERT INTO rate_limit_capacity (id) VALUES (1)",
	} {
		if !strings.Contains(upText, snippet) {
			t.Fatalf("expected up migration to contain %q", snippet)
		}
	}

	downSQL, err := os.ReadFile("000008_rate_limit_buckets.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	if !strings.Contains(string(downSQL), "DROP TABLE IF EXISTS rate_limit_buckets") {
		t.Fatal("expected down migration to remove rate limit buckets")
	}
	if !strings.Contains(string(downSQL), "DROP TABLE IF EXISTS rate_limit_capacity") {
		t.Fatal("expected down migration to remove rate limit capacity")
	}
}
