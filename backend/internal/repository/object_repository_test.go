package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"light-oss/backend/internal/model"
)

func TestLikePrefixPatternUsesPortableEscapeCharacter(t *testing.T) {
	got := likePrefixPattern(`LOVE/%_!\docs`)
	want := `LOVE/!%!_!!\docs%`
	if got != want {
		t.Fatalf("unexpected like prefix pattern: got %q want %q", got, want)
	}
}

func TestListActiveByPrefixForUpdateOrderedAddsUpdateLock(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:object-prefix-lock?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&model.Object{}); err != nil {
		t.Fatalf("migrate objects: %v", err)
	}
	if err := db.Create(testRepositoryObject("bucket", "docs/a.txt")).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}

	sawUpdateLock := false
	if err := db.Callback().Query().Before("gorm:query").Register("test:prefix-update-lock", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Clauses["FOR"]; ok {
			sawUpdateLock = true
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	objects, err := NewObjectRepository(db).ListActiveByPrefixForUpdateOrdered(context.Background(), "bucket", "docs/")
	if err != nil {
		t.Fatalf("list objects for update: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectKey != "docs/a.txt" {
		t.Fatalf("unexpected locked objects: %+v", objects)
	}
	if !sawUpdateLock {
		t.Fatal("expected SELECT query to include a FOR UPDATE clause")
	}
}

func TestHardDeleteByIDsDeletesOnlySelectedRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:object-delete-ids?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&model.Object{}); err != nil {
		t.Fatalf("migrate objects: %v", err)
	}

	first := testRepositoryObject("bucket", "docs/a.txt")
	second := testRepositoryObject("bucket", "docs/b.txt")
	concurrent := testRepositoryObject("bucket", "docs/concurrent.txt")
	if err := db.Create([]*model.Object{first, second, concurrent}).Error; err != nil {
		t.Fatalf("create objects: %v", err)
	}

	repo := NewObjectRepository(db)
	deleted, err := repo.HardDeleteByIDs(context.Background(), []uint64{first.ID, second.ID})
	if err != nil {
		t.Fatalf("delete selected objects: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted rows, got %d", deleted)
	}

	var remaining []model.Object
	if err := db.Order("id ASC").Find(&remaining).Error; err != nil {
		t.Fatalf("list remaining objects: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != concurrent.ID {
		t.Fatalf("expected only concurrently added object to remain, got %+v", remaining)
	}

	deletedOne, err := repo.HardDeleteByID(context.Background(), concurrent.ID)
	if err != nil {
		t.Fatalf("delete selected object: %v", err)
	}
	if !deletedOne {
		t.Fatal("expected selected object to be deleted")
	}
}

func testRepositoryObject(bucketName string, objectKey string) *model.Object {
	return &model.Object{
		BucketName:       bucketName,
		ObjectKey:        objectKey,
		OriginalFilename: objectKey,
		StoragePath:      objectKey,
		ContentType:      "text/plain",
		ETag:             objectKey,
		Visibility:       model.VisibilityPrivate,
	}
}

func TestApplyObjectKeyPrefixFilterUsesPortableEscapeClause(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open dry-run db: %v", err)
	}

	stmt := applyObjectKeyPrefixFilter(db.Model(&model.Object{}), "LOVE/%_!").
		Order("object_key ASC").
		Find(&[]model.Object{}).
		Statement

	sql := stmt.SQL.String()
	if !strings.Contains(sql, "ESCAPE '!'") {
		t.Fatalf("expected portable escape clause, got %q", sql)
	}
	if strings.Contains(sql, `ESCAPE '\'`) {
		t.Fatalf("unexpected mysql-incompatible escape clause in %q", sql)
	}
	if len(stmt.Vars) != 1 {
		t.Fatalf("expected 1 query var, got %d", len(stmt.Vars))
	}
	if got, ok := stmt.Vars[0].(string); !ok || got != `LOVE/!%!_!!%` {
		t.Fatalf("unexpected pattern var %#v", stmt.Vars[0])
	}
}
