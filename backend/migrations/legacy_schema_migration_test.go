package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestLegacySchemaMigrationRemovesRuntimeDeadSchema(t *testing.T) {
	upSQL, err := os.ReadFile("000012_remove_legacy_schema.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}

	upText := string(upSQL)
	for _, snippet := range []string{
		"CHECK (is_deleted = FALSE)",
		"DROP COLUMN is_deleted",
		"DROP TABLE IF EXISTS upload_sessions",
		"DROP TABLE IF EXISTS folder_upload_sessions",
	} {
		if !strings.Contains(upText, snippet) {
			t.Fatalf("expected up migration to contain %q", snippet)
		}
	}

	downSQL, err := os.ReadFile("000012_remove_legacy_schema.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	downText := string(downSQL)
	for _, snippet := range []string{
		"ADD COLUMN is_deleted BOOLEAN NOT NULL DEFAULT FALSE",
		"CREATE TABLE upload_sessions",
		"CREATE TABLE folder_upload_sessions",
	} {
		if !strings.Contains(downText, snippet) {
			t.Fatalf("expected down migration to contain %q", snippet)
		}
	}
}
