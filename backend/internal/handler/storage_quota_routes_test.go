package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
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
