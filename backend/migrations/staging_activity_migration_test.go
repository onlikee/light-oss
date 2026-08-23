package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestStagingActivityMigrationAddsDatabaseClockLease(t *testing.T) {
	upSQL, err := os.ReadFile("000009_staging_activity.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}

	upText := string(upSQL)
	for _, snippet := range []string{
		"ADD COLUMN staging_lease_expires_at DATETIME(6) NULL",
		"idx_storage_blobs_staging_lease",
		"SET staging_lease_expires_at = UTC_TIMESTAMP(6)",
		"WHERE status = 'staging'",
	} {
		if !strings.Contains(upText, snippet) {
			t.Fatalf("expected up migration to contain %q", snippet)
		}
	}

	downSQL, err := os.ReadFile("000009_staging_activity.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	downText := string(downSQL)
	if !strings.Contains(downText, "DROP INDEX idx_storage_blobs_staging_lease") ||
		!strings.Contains(downText, "DROP COLUMN staging_lease_expires_at") {
		t.Fatal("expected down migration to remove the staging lease index and column")
	}
}
