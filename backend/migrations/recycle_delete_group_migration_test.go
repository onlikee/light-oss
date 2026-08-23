package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestRecycleDeleteGroupMigrationBackfillsDeterministicUUIDs(t *testing.T) {
	upSQL, err := os.ReadFile("000010_recycle_delete_groups.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}

	upText := string(upSQL)
	for _, snippet := range []string{
		"ADD COLUMN delete_group_id CHAR(36) NULL",
		"CREATE TEMPORARY TABLE recycle_delete_group_backfill",
		"RIGHT(marker.object_key, CHAR_LENGTH('/.light-oss-folder'))",
		"SHA2(CONCAT('light-oss:recycle-delete-group:', backfill.group_seed_id), 256)",
		"MODIFY COLUMN delete_group_id CHAR(36) NOT NULL",
		"idx_recycle_bin_objects_delete_group (delete_group_id)",
	} {
		if !strings.Contains(upText, snippet) {
			t.Fatalf("expected up migration to contain %q", snippet)
		}
	}
	if strings.Contains(upText, "UUID()") {
		t.Fatal("expected existing rows to receive deterministic delete group IDs")
	}

	downSQL, err := os.ReadFile("000010_recycle_delete_groups.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	downText := string(downSQL)
	for _, snippet := range []string{
		"DROP INDEX idx_recycle_bin_objects_delete_group",
		"DROP COLUMN delete_group_id",
	} {
		if !strings.Contains(downText, snippet) {
			t.Fatalf("expected down migration to contain %q", snippet)
		}
	}
}
