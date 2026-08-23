package integration_test

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
)

func TestMySQLRecycleDeleteGroupMigrationBackfillsDeterministically(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Migrate(9); err != nil {
		t.Fatalf("migrate to version 9: %v", err)
	}

	db := openSQL(t, dsn)
	if _, err := db.Exec(`INSERT INTO buckets (name) VALUES ('recycle-backfill')`); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO recycle_bin_objects (
			bucket_name, object_key, original_filename, storage_path, size,
			content_type, etag, visibility, created_at, deleted_at
		) VALUES
			('recycle-backfill', 'docs/.light-oss-folder', '.light-oss-folder', '', 0, 'application/x-directory', '', 'private', '2026-08-23 12:00:00.000000', '2026-08-23 12:01:00.000000'),
			('recycle-backfill', 'docs/nested/a.txt', 'a.txt', 'objects/a', 1, 'text/plain', 'etag-a', 'private', '2026-08-23 12:00:00.000000', '2026-08-23 12:01:00.000000'),
			('recycle-backfill', 'notes.txt', 'notes.txt', 'objects/notes', 1, 'text/plain', 'etag-notes', 'private', '2026-08-23 12:00:00.000000', '2026-08-23 12:01:00.000000')
	`); err != nil {
		t.Fatalf("seed recycle bin rows: %v", err)
	}

	if err := migrator.Migrate(10); err != nil {
		t.Fatalf("migrate to version 10: %v", err)
	}
	firstGroups := loadMySQLRecycleDeleteGroups(t, db)
	if firstGroups["docs/.light-oss-folder"] != firstGroups["docs/nested/a.txt"] {
		t.Fatalf("expected directory marker and descendant to share a group: %+v", firstGroups)
	}
	if firstGroups["notes.txt"] == firstGroups["docs/.light-oss-folder"] {
		t.Fatalf("expected independent object to receive a distinct group: %+v", firstGroups)
	}
	for objectKey, deleteGroupID := range firstGroups {
		if _, err := uuid.Parse(deleteGroupID); err != nil {
			t.Fatalf("%s delete group ID %q is not a UUID: %v", objectKey, deleteGroupID, err)
		}
	}

	if err := migrator.Migrate(9); err != nil {
		t.Fatalf("roll back delete group migration: %v", err)
	}
	if err := migrator.Migrate(10); err != nil {
		t.Fatalf("reapply delete group migration: %v", err)
	}
	secondGroups := loadMySQLRecycleDeleteGroups(t, db)
	for objectKey, firstGroupID := range firstGroups {
		if secondGroups[objectKey] != firstGroupID {
			t.Fatalf("%s delete group changed from %q to %q after down/up", objectKey, firstGroupID, secondGroups[objectKey])
		}
	}
}

func loadMySQLRecycleDeleteGroups(t *testing.T, db interface {
	Query(query string, args ...any) (*sql.Rows, error)
}) map[string]string {
	t.Helper()

	rows, err := db.Query(`SELECT object_key, delete_group_id FROM recycle_bin_objects ORDER BY id ASC`)
	if err != nil {
		t.Fatalf("query recycle delete groups: %v", err)
	}
	defer rows.Close()

	groups := make(map[string]string)
	for rows.Next() {
		var objectKey string
		var deleteGroupID string
		if err := rows.Scan(&objectKey, &deleteGroupID); err != nil {
			t.Fatalf("scan recycle delete group: %v", err)
		}
		groups[objectKey] = deleteGroupID
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate recycle delete groups: %v", err)
	}

	return groups
}
