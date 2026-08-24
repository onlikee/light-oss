package service

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
)

func TestDeleteLocksSelectedObjectBeforeMovingItToRecycleBin(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:object-delete-selected-id?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&model.Bucket{}, &model.Object{}, &model.RecycleBinObject{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	if err := db.Create(&model.Bucket{Name: "bucket"}).Error; err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	object := &model.Object{
		BucketName:       "bucket",
		ObjectKey:        "docs/a.txt",
		OriginalFilename: "a.txt",
		StoragePath:      "blobs/a",
		ContentType:      "text/plain",
		ETag:             "a",
		Visibility:       model.VisibilityPrivate,
	}
	if err := db.Create(object).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}

	sawUpdateLock := false
	if err := db.Callback().Query().Before("gorm:query").Register("test:object-delete-lock", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Clauses["FOR"]; ok {
			sawUpdateLock = true
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	objectRepo := repository.NewObjectRepository(db)
	objectService := NewObjectService(
		db,
		repository.NewBucketRepository(db),
		objectRepo,
		repository.NewRecycleBinRepository(db),
		nil,
		nil,
	)
	if err := objectService.Delete(context.Background(), "bucket", "docs/a.txt"); err != nil {
		t.Fatalf("delete object: %v", err)
	}
	if !sawUpdateLock {
		t.Fatal("expected object row to be selected with a FOR UPDATE clause")
	}

	var remaining int64
	if err := db.Model(&model.Object{}).Where("id = ?", object.ID).Count(&remaining).Error; err != nil {
		t.Fatalf("count deleted object: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected selected object ID to be deleted, got %d rows", remaining)
	}
}

func TestDeleteFolderDeletesLockedIDsWithoutDeletingLaterMetadata(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:folder-delete-selected-ids?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&model.Bucket{}, &model.Object{}, &model.RecycleBinObject{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	if err := db.Create(&model.Bucket{Name: "bucket"}).Error; err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	if err := db.Create(&model.Object{
		BucketName:       "bucket",
		ObjectKey:        "docs/a.txt",
		OriginalFilename: "a.txt",
		StoragePath:      "blobs/a",
		ContentType:      "text/plain",
		ETag:             "a",
		Visibility:       model.VisibilityPrivate,
	}).Error; err != nil {
		t.Fatalf("create initial object: %v", err)
	}

	sawUpdateLock := false
	if err := db.Callback().Query().Before("gorm:query").Register("test:folder-delete-lock", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Clauses["FOR"]; ok {
			sawUpdateLock = true
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	injected := false
	if err := db.Callback().Create().After("gorm:create").Register("test:concurrent-object", func(tx *gorm.DB) {
		if injected || tx.Statement.Table != "recycle_bin_objects" {
			return
		}
		injected = true
		err := tx.Session(&gorm.Session{NewDB: true}).Exec(`INSERT INTO objects
			(bucket_name, object_key, original_filename, storage_path, size, content_type, etag, visibility, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			"bucket", "docs/concurrent.txt", "concurrent.txt", "blobs/concurrent", 0, "text/plain", "concurrent", model.VisibilityPrivate,
		).Error
		if err != nil {
			tx.AddError(err)
		}
	}); err != nil {
		t.Fatalf("register create callback: %v", err)
	}

	objectRepo := repository.NewObjectRepository(db)
	objectService := NewObjectService(
		db,
		repository.NewBucketRepository(db),
		objectRepo,
		repository.NewRecycleBinRepository(db),
		nil,
		nil,
	)
	if err := objectService.DeleteFolder(context.Background(), "bucket", "docs/", true); err != nil {
		t.Fatalf("delete folder: %v", err)
	}
	if !sawUpdateLock {
		t.Fatal("expected folder rows to be selected with a FOR UPDATE clause")
	}

	remaining, err := objectRepo.ListActiveByPrefixOrdered(context.Background(), "bucket", "docs/")
	if err != nil {
		t.Fatalf("list remaining objects: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ObjectKey != "docs/concurrent.txt" {
		t.Fatalf("expected later metadata to remain active, got %+v", remaining)
	}
}
