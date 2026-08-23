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

func TestProtectedUpdateStorageQuotaRequiresAuth(t *testing.T) {
	router := newTestRouter(t, 1024)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/quota", bytes.NewBufferString(`{"max_bytes":2147483648}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestProtectedUpdateStorageQuotaUpdatesStatsSnapshot(t *testing.T) {
	router := newTestRouter(t, 1024)

	updateStorageQuota(t, router, 2*1024*1024*1024)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/stats", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[systemStatsResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.Storage.MaxBytes != 2*1024*1024*1024 {
		t.Fatalf("expected updated max bytes, got %d", body.Data.Storage.MaxBytes)
	}
}

func TestProtectedUpdateStorageQuotaRejectsBelowUsage(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "quota-usage")
	uploadObject(t, router, "/api/v1/buckets/quota-usage/objects/docs/report.txt", "12345", "public")

	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/quota", bytes.NewBufferString(`{"max_bytes":1}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d, body=%s", rec.Code, rec.Body.String())
	}
	assertAPIErrorCode(t, rec.Body.Bytes(), "storage_limit_below_usage")
}

func TestProtectedSystemStatsReturnsStorageWarningStatus(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "quota-warning")
	uploadObject(t, router, "/api/v1/buckets/quota-warning/objects/docs/report.txt", "12345678", "public")
	updateStorageQuota(t, router, 10)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/stats", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[systemStatsResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)

	if body.Data.Storage.LimitStatus != "warning" {
		t.Fatalf("expected storage limit status warning, got %q", body.Data.Storage.LimitStatus)
	}
}

func TestUploadObjectReturnsStorageLimitExceeded(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "quota-file")
	updateStorageQuota(t, router, 4)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/buckets/quota-file/objects/docs/oversized.txt", bytes.NewBufferString("12345"))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-Object-Visibility", "public")
	req.Header.Set("X-Original-Filename", "oversized.txt")
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInsufficientStorage {
		t.Fatalf("expected 507, got %d, body=%s", rec.Code, rec.Body.String())
	}
	assertAPIErrorCode(t, rec.Body.Bytes(), "storage_limit_exceeded")
	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected no stored files after rejected upload, got %d", files)
	}
}

func TestUploadObjectBatchReturnsStorageLimitExceeded(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "quota-batch")
	updateStorageQuota(t, router, 4)

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/quota-batch/objects/batch",
		map[string]string{
			"prefix":     "docs/",
			"visibility": "public",
			"manifest":   mustMarshalJSON(t, []map[string]string{{"file_field": "file_0", "relative_path": "oversized.txt"}}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "oversized.txt", Content: "12345", ContentType: "text/plain"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInsufficientStorage {
		t.Fatalf("expected 507, got %d, body=%s", rec.Code, rec.Body.String())
	}
	assertAPIErrorCode(t, rec.Body.Bytes(), "storage_limit_exceeded")
	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected no stored files after rejected batch upload, got %d", files)
	}
}

func TestOverwriteUploadReclaimsOldStorageFile(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "overwrite-cleanup")
	uploadObject(t, router, "/api/v1/buckets/overwrite-cleanup/objects/docs/readme.txt", "old", "public")
	if files := countFilesUnderRoot(t, storageRoot); files != 1 {
		t.Fatalf("expected 1 stored file before overwrite, got %d", files)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/buckets/overwrite-cleanup/objects/docs/readme.txt", bytes.NewBufferString("new-content"))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-Object-Visibility", "public")
	req.Header.Set("X-Original-Filename", "readme.txt")
	req.Header.Set("X-Allow-Overwrite", "true")
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 1 {
		t.Fatalf("expected overwrite to keep exactly 1 stored file, got %d", files)
	}
}

func TestDeleteObjectMovesToRecycleBinAndKeepsStorageFile(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "delete-cleanup")
	uploadObject(t, router, "/api/v1/buckets/delete-cleanup/objects/docs/readme.txt", "hello", "public")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/delete-cleanup/objects/docs/readme.txt", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 1 {
		t.Fatalf("expected storage file to remain after soft delete, got %d", files)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 || listBody.Data.Items[0].Path != "docs/readme.txt" {
		t.Fatalf("expected deleted file in recycle bin, got %+v", listBody.Data.Items)
	}
}

func TestListRecycleBinObjectsFiltersByBucket(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "bucket-a")
	createBucket(t, router, "bucket-b")
	uploadObject(t, router, "/api/v1/buckets/bucket-a/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/bucket-b/objects/docs/b.txt", "B", "public")

	deleteAReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/bucket-a/objects/docs/a.txt", nil)
	deleteAReq.Header.Set("Authorization", "Bearer dev-token")
	deleteARec := httptest.NewRecorder()
	router.ServeHTTP(deleteARec, deleteAReq)
	if deleteARec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteARec.Code, deleteARec.Body.String())
	}

	deleteBReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/bucket-b/objects/docs/b.txt", nil)
	deleteBReq.Header.Set("Authorization", "Bearer dev-token")
	deleteBRec := httptest.NewRecorder()
	router.ServeHTTP(deleteBRec, deleteBReq)
	if deleteBRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteBRec.Code, deleteBRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects?bucket=bucket-a", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 recycle bin item, got %+v", listBody.Data.Items)
	}
	if listBody.Data.Items[0].BucketName != "bucket-a" || listBody.Data.Items[0].Path != "docs/a.txt" {
		t.Fatalf("expected only bucket-a items, got %+v", listBody.Data.Items)
	}
}

func TestDeleteFolderMovesObjectsToRecycleBinAndKeepsStorageFiles(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "folder-cleanup")
	createFolder(t, router, "folder-cleanup", "", "docs")
	uploadObject(t, router, "/api/v1/buckets/folder-cleanup/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/folder-cleanup/objects/docs/b.txt", "B", "public")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/folder-cleanup/folders?path=docs/&recursive=true", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 3 {
		t.Fatalf("expected storage files to remain after recursive folder delete, got %d", files)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 recycle bin item, got %d", len(listBody.Data.Items))
	}

	item := listBody.Data.Items[0]
	if item.Path != "docs/" || item.Type != "directory" {
		t.Fatalf("expected docs/ directory recycle bin item, got %+v", item)
	}
	if item.Size != 2 {
		t.Fatalf("expected docs/ directory size to aggregate descendants, got %d", item.Size)
	}
}

func TestDeleteFolderWithoutMarkerMovesSingleDirectoryToRecycleBin(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "folder-no-marker")
	uploadObject(t, router, "/api/v1/buckets/folder-no-marker/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/folder-no-marker/objects/docs/b.txt", "B", "public")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/folder-no-marker/folders?path=docs/&recursive=true", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 2 {
		t.Fatalf("expected storage files to remain after recursive folder delete, got %d", files)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 recycle bin item, got %d", len(listBody.Data.Items))
	}

	item := listBody.Data.Items[0]
	if item.Path != "docs/" || item.Type != "directory" || item.ObjectKey != "docs/.light-oss-folder" {
		t.Fatalf("expected synthetic docs/ directory recycle bin item, got %+v", item)
	}
	if item.Size != 2 {
		t.Fatalf("expected docs/ directory size to aggregate descendants, got %d", item.Size)
	}
}

func TestRecycleBinRestoreRestoresDirectoryWithoutSyntheticMarker(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "restore-folder")
	uploadObject(t, router, "/api/v1/buckets/restore-folder/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/restore-folder/objects/docs/nested/b.txt", "B", "public")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/restore-folder/folders?path=docs/&recursive=true", nil)
	deleteReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 recycle bin item, got %d", len(listBody.Data.Items))
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

	getAReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/restore-folder/objects/docs/a.txt", nil)
	getARec := httptest.NewRecorder()
	router.ServeHTTP(getARec, getAReq)
	if getARec.Code != http.StatusOK || getARec.Body.String() != "A" {
		t.Fatalf("expected restored docs/a.txt, got %d body=%q", getARec.Code, getARec.Body.String())
	}

	getBReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/restore-folder/objects/docs/nested/b.txt", nil)
	getBRec := httptest.NewRecorder()
	router.ServeHTTP(getBRec, getBReq)
	if getBRec.Code != http.StatusOK || getBRec.Body.String() != "B" {
		t.Fatalf("expected restored docs/nested/b.txt, got %d body=%q", getBRec.Code, getBRec.Body.String())
	}

	markerReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/restore-folder/objects/docs/.light-oss-folder", nil)
	markerReq.Header.Set("Authorization", "Bearer dev-token")
	markerRec := httptest.NewRecorder()
	router.ServeHTTP(markerRec, markerReq)
	if markerRec.Code != http.StatusNotFound {
		t.Fatalf("expected synthetic marker to stay absent after restore, got %d", markerRec.Code)
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
		t.Fatalf("expected recycle bin to be empty after restore, got %+v", finalListBody.Data.Items)
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 2 {
		t.Fatalf("expected restored directory to reuse existing storage files, got %d", files)
	}
}

func TestRecycleBinRestoreDirectoryConflictReturnsFailedItem(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "restore-folder-conflict")
	uploadObject(t, router, "/api/v1/buckets/restore-folder-conflict/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/restore-folder-conflict/objects/docs/b.txt", "B", "public")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/restore-folder-conflict/folders?path=docs/&recursive=true", nil)
	deleteReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	uploadObject(t, router, "/api/v1/buckets/restore-folder-conflict/objects/docs/a.txt", "replacement", "public")

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 recycle bin item, got %d", len(listBody.Data.Items))
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
	if restoreBody.Data.RestoredCount != 0 || restoreBody.Data.FailedCount != 1 {
		t.Fatalf("unexpected restore result %+v", restoreBody.Data)
	}
	if len(restoreBody.Data.FailedItems) != 1 || restoreBody.Data.FailedItems[0].Code != "object_exists" {
		t.Fatalf("expected object_exists failure, got %+v", restoreBody.Data.FailedItems)
	}

	getBReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/restore-folder-conflict/objects/docs/b.txt", nil)
	getBRec := httptest.NewRecorder()
	router.ServeHTTP(getBRec, getBReq)
	if getBRec.Code != http.StatusNotFound {
		t.Fatalf("expected docs/b.txt to stay deleted after failed restore, got %d", getBRec.Code)
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
	if len(finalListBody.Data.Items) != 1 || finalListBody.Data.Items[0].Path != "docs/" {
		t.Fatalf("expected directory recycle bin item to remain after failed restore, got %+v", finalListBody.Data.Items)
	}
}

func TestRecycleBinPermanentDeleteDirectoryReclaimsStorageFiles(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "recycle-directory-delete")
	uploadObject(t, router, "/api/v1/buckets/recycle-directory-delete/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/recycle-directory-delete/objects/docs/b.txt", "B", "public")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/recycle-directory-delete/folders?path=docs/&recursive=true", nil)
	deleteReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 recycle bin item, got %d", len(listBody.Data.Items))
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
		t.Fatalf("expected storage files to be removed after permanent directory delete, got %d", files)
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
		t.Fatalf("expected recycle bin to be empty after permanent directory delete, got %+v", finalListBody.Data.Items)
	}
}

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

func TestListRecycleBinObjectsPaginatesLegacyDirectoryItems(t *testing.T) {
	router, _, db := newTestRouterWithStorageRootAndDB(t, 1024)

	createBucket(t, router, "legacy-recycle-pagination")
	createFolder(t, router, "legacy-recycle-pagination", "", "docs")
	uploadObject(t, router, "/api/v1/buckets/legacy-recycle-pagination/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/legacy-recycle-pagination/objects/docs/b.txt", "B", "public")
	uploadObject(t, router, "/api/v1/buckets/legacy-recycle-pagination/objects/notes.txt", "note", "public")

	deleteFileReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/legacy-recycle-pagination/objects/notes.txt", nil)
	deleteFileReq.Header.Set("Authorization", "Bearer dev-token")
	deleteFileRec := httptest.NewRecorder()
	router.ServeHTTP(deleteFileRec, deleteFileReq)
	if deleteFileRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteFileRec.Code, deleteFileRec.Body.String())
	}

	seedLegacyRecycleBinDirectory(t, db, "legacy-recycle-pagination", "docs/", time.Now().UTC().Add(10*time.Millisecond))

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
		t.Fatalf("expected next cursor for legacy logical directory pagination")
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

func TestRecycleBinRestoreRestoresLegacyDirectoryGroup(t *testing.T) {
	router, storageRoot, db := newTestRouterWithStorageRootAndDB(t, 1024)

	createBucket(t, router, "legacy-restore-folder")
	createFolder(t, router, "legacy-restore-folder", "", "docs")
	uploadObject(t, router, "/api/v1/buckets/legacy-restore-folder/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/legacy-restore-folder/objects/docs/nested/b.txt", "B", "public")

	seedLegacyRecycleBinDirectory(t, db, "legacy-restore-folder", "docs/", time.Now().UTC())

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
		t.Fatalf("expected one legacy directory recycle bin item, got %+v", listBody.Data.Items)
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

	getAReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/legacy-restore-folder/objects/docs/a.txt", nil)
	getARec := httptest.NewRecorder()
	router.ServeHTTP(getARec, getAReq)
	if getARec.Code != http.StatusOK || getARec.Body.String() != "A" {
		t.Fatalf("expected restored docs/a.txt, got %d body=%q", getARec.Code, getARec.Body.String())
	}

	getBReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/legacy-restore-folder/objects/docs/nested/b.txt", nil)
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
		t.Fatalf("expected recycle bin to be empty after legacy restore, got %+v", finalListBody.Data.Items)
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 3 {
		t.Fatalf("expected restored legacy directory to reuse storage files, got %d", files)
	}
}

func TestRecycleBinPermanentDeleteLegacyDirectoryReclaimsStorageFiles(t *testing.T) {
	router, storageRoot, db := newTestRouterWithStorageRootAndDB(t, 1024)

	createBucket(t, router, "legacy-directory-delete")
	createFolder(t, router, "legacy-directory-delete", "", "docs")
	uploadObject(t, router, "/api/v1/buckets/legacy-directory-delete/objects/docs/a.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/legacy-directory-delete/objects/docs/b.txt", "B", "public")

	seedLegacyRecycleBinDirectory(t, db, "legacy-directory-delete", "docs/", time.Now().UTC())

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
		t.Fatalf("expected one legacy directory recycle bin item, got %+v", listBody.Data.Items)
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
		t.Fatalf("expected legacy directory delete to reclaim storage files, got %d", files)
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
		t.Fatalf("expected recycle bin to be empty after legacy permanent delete, got %+v", finalListBody.Data.Items)
	}
}

func TestRecycleBinRestoreRestoresObject(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "restore-bucket")
	uploadObject(t, router, "/api/v1/buckets/restore-bucket/objects/docs/readme.txt", "hello", "public")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/restore-bucket/objects/docs/readme.txt", nil)
	deleteReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	var recycleItem recycleBinObjectResponse
	{
		listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
		listReq.Header.Set("Authorization", "Bearer dev-token")
		listRec := httptest.NewRecorder()
		router.ServeHTTP(listRec, listReq)
		if listRec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
		}

		var listBody apiEnvelope[recycleBinListResponse]
		decodeJSON(t, listRec.Body.Bytes(), &listBody)
		if len(listBody.Data.Items) != 1 {
			t.Fatalf("expected 1 recycle bin item, got %d", len(listBody.Data.Items))
		}
		recycleItem = listBody.Data.Items[0]
	}

	restoreReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/recycle-bin/objects/restore",
		bytes.NewBufferString(`{"item_ids":[`+strconv.FormatUint(recycleItem.ID, 10)+`]}`),
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

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/restore-bucket/objects/docs/readme.txt", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected restored object to be downloadable, got %d", getRec.Code)
	}
	if body := getRec.Body.String(); body != "hello" {
		t.Fatalf("unexpected restored body %q", body)
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 1 {
		t.Fatalf("expected restored object to reuse existing storage file, got %d", files)
	}
}

func TestRecycleBinRestoreConflictReturnsFailedItem(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "restore-conflict")
	uploadObject(t, router, "/api/v1/buckets/restore-conflict/objects/docs/readme.txt", "hello", "public")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/restore-conflict/objects/docs/readme.txt", nil)
	deleteReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	uploadObject(t, router, "/api/v1/buckets/restore-conflict/objects/docs/readme.txt", "replacement", "public")

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 recycle bin item, got %d", len(listBody.Data.Items))
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
	if restoreBody.Data.RestoredCount != 0 || restoreBody.Data.FailedCount != 1 {
		t.Fatalf("unexpected restore result %+v", restoreBody.Data)
	}
	if len(restoreBody.Data.FailedItems) != 1 || restoreBody.Data.FailedItems[0].Code != "object_exists" {
		t.Fatalf("expected object_exists failure, got %+v", restoreBody.Data.FailedItems)
	}

	var restoreRaw apiEnvelope[map[string]any]
	decodeJSON(t, restoreRec.Body.Bytes(), &restoreRaw)
	if _, exists := restoreRaw.Data["restored_count"]; !exists {
		t.Fatalf("expected restored_count field to be present, got %+v", restoreRaw.Data)
	}
}

func TestRecycleBinPermanentDeleteReclaimsStorageFile(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "recycle-delete")
	uploadObject(t, router, "/api/v1/buckets/recycle-delete/objects/docs/readme.txt", "hello", "public")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/recycle-delete/objects/docs/readme.txt", nil)
	deleteReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 recycle bin item, got %d", len(listBody.Data.Items))
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
		t.Fatalf("expected storage file to be removed after permanent delete, got %d", files)
	}
}

func TestDeleteBucketClearsRecycleBinAndReclaimsStorageFiles(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "bucket-delete")
	uploadObject(t, router, "/api/v1/buckets/bucket-delete/objects/docs/readme.txt", "hello", "public")

	deleteObjectReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/bucket-delete/objects/docs/readme.txt", nil)
	deleteObjectReq.Header.Set("Authorization", "Bearer dev-token")
	deleteObjectRec := httptest.NewRecorder()
	router.ServeHTTP(deleteObjectRec, deleteObjectReq)
	if deleteObjectRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteObjectRec.Code, deleteObjectRec.Body.String())
	}

	deleteBucketReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/bucket-delete", nil)
	deleteBucketReq.Header.Set("Authorization", "Bearer dev-token")
	deleteBucketRec := httptest.NewRecorder()
	router.ServeHTTP(deleteBucketRec, deleteBucketReq)
	if deleteBucketRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteBucketRec.Code, deleteBucketRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recycle-bin/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[recycleBinListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 0 {
		t.Fatalf("expected recycle bin to be empty after bucket delete, got %+v", listBody.Data.Items)
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected bucket delete to reclaim storage files, got %d", files)
	}
}

func TestCreateFolderReturnsStorageLimitExceededWhenUsageIsAtLimit(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "quota-folder")
	uploadObject(t, router, "/api/v1/buckets/quota-folder/objects/docs/report.txt", "12345", "public")
	updateStorageQuota(t, router, 5)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/buckets/quota-folder/folders", bytes.NewBufferString(`{"prefix":"","name":"empty"}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInsufficientStorage {
		t.Fatalf("expected 507, got %d, body=%s", rec.Code, rec.Body.String())
	}
	assertAPIErrorCode(t, rec.Body.Bytes(), "storage_limit_exceeded")
	if files := countFilesUnderRoot(t, storageRoot); files != 1 {
		t.Fatalf("expected only the original file to remain after rejected folder create, got %d", files)
	}
}

func seedLegacyRecycleBinDirectory(
	t *testing.T,
	db *gorm.DB,
	bucketName string,
	folderPath string,
	deletedAt time.Time,
) {
	t.Helper()

	var objects []model.Object
	if err := db.
		Where("bucket_name = ? AND is_deleted = ?", bucketName, false).
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
		t.Fatalf("create legacy recycle bin rows: %v", err)
	}
	if err := db.
		Where("bucket_name = ? AND is_deleted = ?", bucketName, false).
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

func updateStorageQuota(t *testing.T, router http.Handler, maxBytes int64) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/quota", bytes.NewBufferString(`{"max_bytes":`+strconv.FormatInt(maxBytes, 10)+`}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update storage quota expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}
}
