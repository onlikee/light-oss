package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestStorageIdentityMigrationAddsPersistentBinding(t *testing.T) {
	upSQL, err := os.ReadFile("000011_storage_identity.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	if !strings.Contains(string(upSQL), "ADD COLUMN storage_id VARCHAR(36) NULL") {
		t.Fatal("expected up migration to add storage identity")
	}
	if !strings.Contains(string(upSQL), "SET reconciled_at = NULL") {
		t.Fatal("expected storage identity migration to require one verified reconciliation")
	}

	downSQL, err := os.ReadFile("000011_storage_identity.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	if !strings.Contains(string(downSQL), "DROP COLUMN storage_id") {
		t.Fatal("expected down migration to remove storage identity")
	}
}
