package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestRecycleBinMigrationBackfillsDeletedObjects(t *testing.T) {
	upSQL, err := os.ReadFile("000006_recycle_bin.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}

	upText := string(upSQL)
	requiredSnippets := []string{
		"CREATE TABLE recycle_bin_objects",
		"INSERT INTO recycle_bin_objects",
		"FROM objects",
		"WHERE is_deleted = TRUE",
		"DELETE FROM objects WHERE is_deleted = TRUE",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(upText, snippet) {
			t.Fatalf("expected up migration to contain %q", snippet)
		}
	}
	if strings.Contains(upText, "DEFAULT CHARSET=") || strings.Contains(upText, "COLLATE=") {
		t.Fatalf("expected up migration to inherit the database character set and collation so bucket foreign keys stay compatible")
	}

	downSQL, err := os.ReadFile("000006_recycle_bin.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	downText := string(downSQL)
	requiredDownSnippets := []string{
		"INSERT INTO objects",
		"FROM recycle_bin_objects AS recycle;",
		"DROP TABLE IF EXISTS recycle_bin_objects",
	}
	for _, snippet := range requiredDownSnippets {
		if !strings.Contains(downText, snippet) {
			t.Fatalf("expected down migration to contain %q", snippet)
		}
	}
	if strings.Contains(downText, "WHERE NOT EXISTS") {
		t.Fatalf("expected down migration to fail on rollback conflicts instead of skipping recycle bin rows")
	}
}
