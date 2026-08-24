package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUploadObjectBatchRejectsInvalidFinalObjectKeyFromPrefix(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 8*1024)

	createBucket(t, router, "batch-prefix-bucket")

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/batch-prefix-bucket/objects/batch",
		map[string]string{
			"prefix": "/",
			"manifest": mustMarshalJSON(t, []map[string]string{
				{"file_field": "file_0", "relative_path": "assets/readme.txt"},
			}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "readme.txt", Content: "hello world"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[uploadBatchResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "invalid_batch_manifest" {
		t.Fatalf("expected invalid_batch_manifest, got %+v", body.Error)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/batch-prefix-bucket/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 0 {
		t.Fatalf("expected no persisted objects after invalid final key, got %+v", listBody.Data.Items)
	}

	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected no stored files after invalid final key, got %d", files)
	}
}

func TestUploadObjectBatchRejectsOverlongFinalObjectKey(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 8*1024)

	createBucket(t, router, "batch-long-key-bucket")

	prefix := strings.Repeat("a", 508) + "/"
	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/batch-long-key-bucket/objects/batch",
		map[string]string{
			"prefix": prefix,
			"manifest": mustMarshalJSON(t, []map[string]string{
				{"file_field": "file_0", "relative_path": "b.txt"},
			}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "b.txt", Content: "hello world"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[uploadBatchResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "invalid_batch_manifest" {
		t.Fatalf("expected invalid_batch_manifest, got %+v", body.Error)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/batch-long-key-bucket/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 0 {
		t.Fatalf("expected no persisted objects after overlong final key, got %+v", listBody.Data.Items)
	}

	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected no stored files after overlong final key, got %d", files)
	}
}

func TestUploadObjectBatchRollsBackAndCleansStorage(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 8*1024)

	createBucket(t, router, "batch-rollback-bucket")

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/batch-rollback-bucket/objects/batch",
		map[string]string{
			"manifest": mustMarshalJSON(t, []map[string]string{
				{"file_field": "file_0", "relative_path": "assets/readme.txt"},
				{"file_field": "file_1", "relative_path": "/invalid.txt"},
			}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "readme.txt", Content: "hello world"},
			"file_1": {Filename: "invalid.txt", Content: "bad"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[uploadBatchResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "invalid_batch_manifest" {
		t.Fatalf("expected invalid_batch_manifest, got %+v", body.Error)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/batch-rollback-bucket/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 0 {
		t.Fatalf("expected no persisted objects after rollback, got %+v", listBody.Data.Items)
	}

	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected no stored files after rollback, got %d", files)
	}
}

func TestUploadObjectBatchBucketNotFound(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/missing-bucket/objects/batch",
		map[string]string{
			"manifest": mustMarshalJSON(t, []map[string]string{
				{"file_field": "file_0", "relative_path": "assets/readme.txt"},
			}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "readme.txt", Content: "hello"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadObjectBatchSizeLimit(t *testing.T) {
	router := newTestRouter(t, 64)

	createBucket(t, router, "batch-limit-bucket")

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/batch-limit-bucket/objects/batch",
		map[string]string{
			"manifest": mustMarshalJSON(t, []map[string]string{
				{"file_field": "file_0", "relative_path": "assets/big.txt"},
			}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "big.txt", Content: strings.Repeat("a", 256)},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d, body=%s", rec.Code, rec.Body.String())
	}
}
