package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"light-oss/backend/internal/model"
)

func TestListRecycleBinObjectsPaginatesLogicalDirectoryItems(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "recycle-pagination")
	uploadObject(t, router, "/api/v1/buckets/recycle-pagination/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/recycle-pagination/objects/docs/b.txt", "B", "public")
	uploadObject(t, router, "/api/v1/buckets/recycle-pagination/objects/notes.txt", "note", "public")

	deleteFileReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/recycle-pagination/objects/notes.txt", nil)
	deleteFileReq.Header.Set("Authorization", "Bearer dev-token")
	deleteFileRec := httptest.NewRecorder()
	router.ServeHTTP(deleteFileRec, deleteFileReq)
	if deleteFileRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteFileRec.Code, deleteFileRec.Body.String())
	}

	time.Sleep(10 * time.Millisecond)

	deleteFolderReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/recycle-pagination/folders?path=docs/&recursive=true", nil)
	deleteFolderReq.Header.Set("Authorization", "Bearer dev-token")
	deleteFolderRec := httptest.NewRecorder()
	router.ServeHTTP(deleteFolderRec, deleteFolderReq)
	if deleteFolderRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteFolderRec.Code, deleteFolderRec.Body.String())
	}

	firstPageReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects?limit=1", nil)
	firstPageReq.Header.Set("Authorization", "Bearer dev-token")
	firstPageRec := httptest.NewRecorder()
	router.ServeHTTP(firstPageRec, firstPageReq)
	if firstPageRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", firstPageRec.Code, firstPageRec.Body.String())
	}

	var firstPageBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, firstPageRec.Body.Bytes(), &firstPageBody)
	if len(firstPageBody.Data.Items) != 1 || firstPageBody.Data.Items[0].Path != "docs/" {
		t.Fatalf("expected first page to contain only docs/, got %+v", firstPageBody.Data.Items)
	}
	if firstPageBody.Data.NextCursor == "" {
		t.Fatalf("expected next cursor for logical recycle bin pagination")
	}

	secondPageReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/recycle-bin/objects?limit=1&cursor="+firstPageBody.Data.NextCursor,
		nil,
	)
	secondPageReq.Header.Set("Authorization", "Bearer dev-token")
	secondPageRec := httptest.NewRecorder()
	router.ServeHTTP(secondPageRec, secondPageReq)
	if secondPageRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", secondPageRec.Code, secondPageRec.Body.String())
	}

	var secondPageBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, secondPageRec.Body.Bytes(), &secondPageBody)
	if len(secondPageBody.Data.Items) != 1 || secondPageBody.Data.Items[0].Path != "notes.txt" {
		t.Fatalf("expected second page to contain notes.txt, got %+v", secondPageBody.Data.Items)
	}
}

func TestListRecycleBinObjectsPaginatesExistingDirectoryItems(t *testing.T) {
	router, _, db := newTestRouterWithStorageRootAndDB(t, 1024)

	createBucket(t, router, "existing-recycle-pagination")
	createFolder(t, router, "existing-recycle-pagination", "", "docs")
	uploadObject(t, router, "/api/v1/buckets/existing-recycle-pagination/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/existing-recycle-pagination/objects/docs/b.txt", "B", "public")
	uploadObject(t, router, "/api/v1/buckets/existing-recycle-pagination/objects/notes.txt", "note", "public")

	deleteFileReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/existing-recycle-pagination/objects/notes.txt", nil)
	deleteFileReq.Header.Set("Authorization", "Bearer dev-token")
	deleteFileRec := httptest.NewRecorder()
	router.ServeHTTP(deleteFileRec, deleteFileReq)
	if deleteFileRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteFileRec.Code, deleteFileRec.Body.String())
	}

	seedExistingRecycleBinDirectory(t, db, "existing-recycle-pagination", "docs/", time.Now().UTC().Add(10*time.Millisecond))

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 2 {
		t.Fatalf("expected 2 logical recycle bin items, got %+v", listBody.Data.Items)
	}
	if listBody.Data.Items[0].Path != "docs/" || listBody.Data.Items[1].Path != "notes.txt" {
		t.Fatalf("expected docs/ then notes.txt, got %+v", listBody.Data.Items)
	}

	firstPageReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects?limit=1", nil)
	firstPageReq.Header.Set("Authorization", "Bearer dev-token")
	firstPageRec := httptest.NewRecorder()
	router.ServeHTTP(firstPageRec, firstPageReq)
	if firstPageRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", firstPageRec.Code, firstPageRec.Body.String())
	}

	var firstPageBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, firstPageRec.Body.Bytes(), &firstPageBody)
	if len(firstPageBody.Data.Items) != 1 || firstPageBody.Data.Items[0].Path != "docs/" {
		t.Fatalf("expected first page to contain docs/, got %+v", firstPageBody.Data.Items)
	}
	if firstPageBody.Data.NextCursor == "" {
		t.Fatalf("expected next cursor for existing logical directory pagination")
	}

	secondPageReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/recycle-bin/objects?limit=1&cursor="+firstPageBody.Data.NextCursor,
		nil,
	)
	secondPageReq.Header.Set("Authorization", "Bearer dev-token")
	secondPageRec := httptest.NewRecorder()
	router.ServeHTTP(secondPageRec, secondPageReq)
	if secondPageRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", secondPageRec.Code, secondPageRec.Body.String())
	}

	var secondPageBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, secondPageRec.Body.Bytes(), &secondPageBody)
	if len(secondPageBody.Data.Items) != 1 || secondPageBody.Data.Items[0].Path != "notes.txt" {
		t.Fatalf("expected second page to contain notes.txt, got %+v", secondPageBody.Data.Items)
	}
}

func TestRecycleBinRestoreRestoresExistingDirectoryGroup(t *testing.T) {
	router, storageRoot, db := newTestRouterWithStorageRootAndDB(t, 1024)

	createBucket(t, router, "existing-restore-folder")
	createFolder(t, router, "existing-restore-folder", "", "docs")
	uploadObject(t, router, "/api/v1/buckets/existing-restore-folder/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/existing-restore-folder/objects/docs/nested/b.txt", "B", "public")

	seedExistingRecycleBinDirectory(t, db, "existing-restore-folder", "docs/", time.Now().UTC())

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 || listBody.Data.Items[0].Path != "docs/" {
		t.Fatalf("expected one existing directory recycle bin item, got %+v", listBody.Data.Items)
	}

	restoreReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/recycle-bin/objects/restore",
		bytes.NewBufferString(`{"item_ids":[`+strconv.FormatUint(listBody.Data.Items[0].ID, 10)+`]}`),
	)
	restoreReq.Header.Set("Authorization", "Bearer dev-token")
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreRec := httptest.NewRecorder()
	router.ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", restoreRec.Code, restoreRec.Body.String())
	}

	var restoreBody apiEnvelope[recycleBinBatchResponse]
	decodeJSON(t, restoreRec.Body.Bytes(), &restoreBody)
	if restoreBody.Data.RestoredCount != 1 || restoreBody.Data.FailedCount != 0 {
		t.Fatalf("unexpected restore result %+v", restoreBody.Data)
	}

	getAReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/existing-restore-folder/objects/docs/a.txt", nil)
	getARec := httptest.NewRecorder()
	router.ServeHTTP(getARec, getAReq)
	if getARec.Code != http.StatusOK || getARec.Body.String() != "A" {
		t.Fatalf("expected restored docs/a.txt, got %d body=%q", getARec.Code, getARec.Body.String())
	}

	getBReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/existing-restore-folder/objects/docs/nested/b.txt", nil)
	getBRec := httptest.NewRecorder()
	router.ServeHTTP(getBRec, getBReq)
	if getBRec.Code != http.StatusOK || getBRec.Body.String() != "B" {
		t.Fatalf("expected restored docs/nested/b.txt, got %d body=%q", getBRec.Code, getBRec.Body.String())
	}

	finalListReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	finalListReq.Header.Set("Authorization", "Bearer dev-token")
	finalListRec := httptest.NewRecorder()
	router.ServeHTTP(finalListRec, finalListReq)
	if finalListRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", finalListRec.Code, finalListRec.Body.String())
	}

	var finalListBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, finalListRec.Body.Bytes(), &finalListBody)
	if len(finalListBody.Data.Items) != 0 {
		t.Fatalf("expected recycle bin to be empty after existing-group restore, got %+v", finalListBody.Data.Items)
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 3 {
		t.Fatalf("expected restored existing directory to reuse storage files, got %d", files)
	}
}

func TestRecycleBinPermanentDeleteExistingDirectoryReclaimsStorageFiles(t *testing.T) {
	router, storageRoot, db := newTestRouterWithStorageRootAndDB(t, 1024)

	createBucket(t, router, "existing-directory-delete")
	createFolder(t, router, "existing-directory-delete", "", "docs")
	uploadObject(t, router, "/api/v1/buckets/existing-directory-delete/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/existing-directory-delete/objects/docs/b.txt", "B", "public")

	seedExistingRecycleBinDirectory(t, db, "existing-directory-delete", "docs/", time.Now().UTC())

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 || listBody.Data.Items[0].Path != "docs/" {
		t.Fatalf("expected one existing directory recycle bin item, got %+v", listBody.Data.Items)
	}

	deleteRecycleReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/recycle-bin/objects/batch-delete",
		bytes.NewBufferString(`{"item_ids":[`+strconv.FormatUint(listBody.Data.Items[0].ID, 10)+`]}`),
	)
	deleteRecycleReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRecycleReq.Header.Set("Content-Type", "application/json")
	deleteRecycleRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRecycleRec, deleteRecycleReq)
	if deleteRecycleRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", deleteRecycleRec.Code, deleteRecycleRec.Body.String())
	}

	var deleteBody apiEnvelope[recycleBinBatchResponse]
	decodeJSON(t, deleteRecycleRec.Body.Bytes(), &deleteBody)
	if deleteBody.Data.DeletedCount != 1 || deleteBody.Data.FailedCount != 0 {
		t.Fatalf("unexpected delete result %+v", deleteBody.Data)
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected existing directory delete to reclaim storage files, got %d", files)
	}

	finalListReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	finalListReq.Header.Set("Authorization", "Bearer dev-token")
	finalListRec := httptest.NewRecorder()
	router.ServeHTTP(finalListRec, finalListReq)
	if finalListRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", finalListRec.Code, finalListRec.Body.String())
	}

	var finalListBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, finalListRec.Body.Bytes(), &finalListBody)
	if len(finalListBody.Data.Items) != 0 {
		t.Fatalf("expected recycle bin to be empty after existing-group permanent delete, got %+v", finalListBody.Data.Items)
	}
}

func seedExistingRecycleBinDirectory(
	t *testing.T,
	db *gorm.DB,
	bucketName string,
	folderPath string,
	deletedAt time.Time,
) {
	t.Helper()

	var objects []model.Object
	if err := db.
		Where("bucket_name = ?", bucketName).
		Where("object_key LIKE ?", folderPath+"%").
		Order("object_key ASC").
		Find(&objects).Error; err != nil {
		t.Fatalf("list active folder objects: %v", err)
	}
	if len(objects) == 0 {
		t.Fatalf("expected active objects for %s%s", bucketName, folderPath)
	}

	markerKey := folderPath + ".light-oss-folder"
	recycleItems := make([]model.RecycleBinObject, 0, len(objects))
	deleteGroupID := uuid.NewString()
	markerFound := false
	for _, object := range objects {
		if object.ObjectKey != markerKey {
			continue
		}

		recycleItems = append(recycleItems, recycleBinObjectFromActiveObject(object, deletedAt, deleteGroupID))
		markerFound = true
		break
	}
	if !markerFound {
		t.Fatalf("expected folder marker %q in active objects", markerKey)
	}

	for _, object := range objects {
		if object.ObjectKey == markerKey {
			continue
		}

		recycleItems = append(recycleItems, recycleBinObjectFromActiveObject(object, deletedAt, deleteGroupID))
	}

	if err := db.Create(&recycleItems).Error; err != nil {
		t.Fatalf("create existing recycle bin rows: %v", err)
	}
	if err := db.
		Where("bucket_name = ?", bucketName).
		Where("object_key LIKE ?", folderPath+"%").
		Delete(&model.Object{}).Error; err != nil {
		t.Fatalf("delete active folder objects: %v", err)
	}
}

func recycleBinObjectFromActiveObject(
	object model.Object,
	deletedAt time.Time,
	deleteGroupID string,
) model.RecycleBinObject {
	return model.RecycleBinObject{
		DeleteGroupID:    deleteGroupID,
		BucketName:       object.BucketName,
		ObjectKey:        object.ObjectKey,
		OriginalFilename: object.OriginalFilename,
		StoragePath:      object.StoragePath,
		Size:             object.Size,
		ContentType:      object.ContentType,
		ETag:             object.ETag,
		FileFingerprint:  object.FileFingerprint,
		Visibility:       object.Visibility,
		CreatedAt:        object.CreatedAt,
		DeletedAt:        deletedAt,
	}
}
