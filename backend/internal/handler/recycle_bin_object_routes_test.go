package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

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
