package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
)

func TestRecycleBinDeleteGroupsSeparateOverlappingPrefixesWithSameDeletedAt(t *testing.T) {
	deletedAt := time.Date(2026, time.August, 23, 12, 0, 0, 123000, time.UTC)
	outerGroupID := uuid.NewString()
	innerGroupID := uuid.NewString()
	items := []model.RecycleBinObject{
		newRecycleBinGroupTestItem(4, innerGroupID, "docs/nested/.light-oss-folder", "", 0, deletedAt),
		newRecycleBinGroupTestItem(3, innerGroupID, "docs/nested/b.txt", "objects/b", 2, deletedAt),
		newRecycleBinGroupTestItem(2, outerGroupID, "docs/.light-oss-folder", "", 0, deletedAt),
		newRecycleBinGroupTestItem(1, outerGroupID, "docs/a.txt", "objects/a", 1, deletedAt),
	}

	logicalItems := recycleBinLogicalItemsFromRawItems(items)
	if len(logicalItems) != 2 {
		t.Fatalf("logical items = %d, want 2: %+v", len(logicalItems), logicalItems)
	}
	if logicalItems[0].Item.Path != "docs/nested/" || logicalItems[0].Item.Size != 2 {
		t.Fatalf("unexpected inner directory item: %+v", logicalItems[0].Item)
	}
	if logicalItems[1].Item.Path != "docs/" || logicalItems[1].Item.Size != 1 {
		t.Fatalf("unexpected outer directory item: %+v", logicalItems[1].Item)
	}

	db, _ := newStorageLifecycleTestDB(t, 1024)
	if err := db.Create(&model.Bucket{Name: "recycle-groups", CreatedAt: deletedAt, UpdatedAt: deletedAt}).Error; err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	for index := range items {
		items[index].BucketName = "recycle-groups"
	}
	if err := db.Create(&items).Error; err != nil {
		t.Fatalf("create recycle bin groups: %v", err)
	}

	recycleRepo := repository.NewRecycleBinRepository(db)
	actionItems, err := loadRecycleBinActionItems(context.Background(), recycleRepo, items[2])
	if err != nil {
		t.Fatalf("load outer directory action items: %v", err)
	}
	if len(actionItems) != 2 {
		t.Fatalf("outer action group size = %d, want 2: %+v", len(actionItems), actionItems)
	}
	for _, actionItem := range actionItems {
		if actionItem.DeleteGroupID != outerGroupID {
			t.Fatalf("outer directory action crossed into delete group %q", actionItem.DeleteGroupID)
		}
	}
}

func newRecycleBinGroupTestItem(
	id uint64,
	deleteGroupID string,
	objectKey string,
	storagePath string,
	size int64,
	deletedAt time.Time,
) model.RecycleBinObject {
	return model.RecycleBinObject{
		ID:               id,
		DeleteGroupID:    deleteGroupID,
		BucketName:       "placeholder",
		ObjectKey:        objectKey,
		OriginalFilename: objectKey,
		StoragePath:      storagePath,
		Size:             size,
		ContentType:      "application/octet-stream",
		ETag:             "etag",
		Visibility:       model.VisibilityPrivate,
		CreatedAt:        deletedAt,
		DeletedAt:        deletedAt,
	}
}
