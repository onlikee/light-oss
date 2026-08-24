package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

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
