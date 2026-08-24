package handler_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUploadAndDownloadPublicObject(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "public-bucket")

	req := httptest.NewRequest(http.MethodPut, "/api/v1/buckets/public-bucket/objects/docs/readme.txt", strings.NewReader("hello world"))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-Object-Visibility", "public")
	req.Header.Set("X-Original-Filename", "readme.txt")
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var uploadBody apiEnvelope[objectResponse]
	decodeJSON(t, rec.Body.Bytes(), &uploadBody)
	if uploadBody.Data.OriginalFilename != "readme.txt" {
		t.Fatalf("unexpected original filename %q", uploadBody.Data.OriginalFilename)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/public-bucket/objects/docs/readme.txt", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRec.Code)
	}
	if body := getRec.Body.String(); body != "hello world" {
		t.Fatalf("unexpected body %q", body)
	}
	if got := getRec.Header().Get("ETag"); got == "" {
		t.Fatalf("expected etag header")
	}

	headReq := httptest.NewRequest(http.MethodHead, "/api/v1/buckets/public-bucket/objects/docs/readme.txt", nil)
	headRec := httptest.NewRecorder()
	router.ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", headRec.Code)
	}
}

func TestUploadObjectConflictRequiresAllowOverwriteHeader(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "overwrite-single")
	uploadObject(t, router, "/api/v1/buckets/overwrite-single/objects/docs/readme.txt", "old", "public")

	conflictReq := httptest.NewRequest(http.MethodPut, "/api/v1/buckets/overwrite-single/objects/docs/readme.txt", strings.NewReader("new"))
	conflictReq.Header.Set("Authorization", "Bearer dev-token")
	conflictReq.Header.Set("X-Object-Visibility", "public")
	conflictReq.Header.Set("X-Original-Filename", "readme.txt")
	conflictReq.Header.Set("Content-Type", "text/plain")
	conflictRec := httptest.NewRecorder()
	router.ServeHTTP(conflictRec, conflictReq)

	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d, body=%s", conflictRec.Code, conflictRec.Body.String())
	}
	assertAPIErrorCode(t, conflictRec.Body.Bytes(), "object_exists")

	overwriteReq := httptest.NewRequest(http.MethodPut, "/api/v1/buckets/overwrite-single/objects/docs/readme.txt", strings.NewReader("new"))
	overwriteReq.Header.Set("Authorization", "Bearer dev-token")
	overwriteReq.Header.Set("X-Object-Visibility", "public")
	overwriteReq.Header.Set("X-Original-Filename", "readme.txt")
	overwriteReq.Header.Set("X-Allow-Overwrite", "true")
	overwriteReq.Header.Set("Content-Type", "text/plain")
	overwriteRec := httptest.NewRecorder()
	router.ServeHTTP(overwriteRec, overwriteReq)

	if overwriteRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", overwriteRec.Code, overwriteRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/overwrite-single/objects/docs/readme.txt", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRec.Code)
	}
	if body := getRec.Body.String(); body != "new" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestPrivateObjectRequiresAuthOrSignature(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "private-bucket")
	uploadObject(t, router, "/api/v1/buckets/private-bucket/objects/secrets/report.txt", "very secret", "private")

	anonymousReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/private-bucket/objects/secrets/report.txt", nil)
	anonymousRec := httptest.NewRecorder()
	router.ServeHTTP(anonymousRec, anonymousReq)
	if anonymousRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", anonymousRec.Code)
	}

	authReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/private-bucket/objects/secrets/report.txt", nil)
	authReq.Header.Set("Authorization", "Bearer dev-token")
	authRec := httptest.NewRecorder()
	router.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", authRec.Code)
	}

	signReq := httptest.NewRequest(http.MethodPost, "/api/v1/sign/download", bytes.NewBufferString(`{"bucket":"private-bucket","object_key":"secrets/report.txt","expires_in_seconds":300}`))
	signReq.Header.Set("Authorization", "Bearer dev-token")
	signReq.Header.Set("Content-Type", "application/json")
	signRec := httptest.NewRecorder()
	router.ServeHTTP(signRec, signReq)
	if signRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", signRec.Code, signRec.Body.String())
	}

	var signBody apiEnvelope[signResponse]
	decodeJSON(t, signRec.Body.Bytes(), &signBody)
	parsed, err := url.Parse(signBody.Data.Path)
	if err != nil {
		t.Fatalf("parse signed path: %v", err)
	}
	if parsed.IsAbs() || parsed.Host != "" {
		t.Fatalf("expected relative signed path, got %q", signBody.Data.Path)
	}
	if !strings.HasPrefix(parsed.Path, "/api/v1/") {
		t.Fatalf("expected signed API path, got %q", signBody.Data.Path)
	}

	signedReq := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	signedRec := httptest.NewRecorder()
	router.ServeHTTP(signedRec, signedReq)
	if signedRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", signedRec.Code, signedRec.Body.String())
	}

	query := parsed.Query()
	query.Set("signature", "broken")
	parsed.RawQuery = query.Encode()
	tamperedReq := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	tamperedRec := httptest.NewRecorder()
	router.ServeHTTP(tamperedRec, tamperedReq)
	if tamperedRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", tamperedRec.Code)
	}
}

func TestListObjectsPaginationAndPrefix(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "list-bucket")
	uploadObject(t, router, "/api/v1/buckets/list-bucket/objects/docs/a.txt", "A", "public")
	time.Sleep(2 * time.Millisecond)
	uploadObject(t, router, "/api/v1/buckets/list-bucket/objects/docs/b.txt", "B", "public")
	time.Sleep(2 * time.Millisecond)
	uploadObject(t, router, "/api/v1/buckets/list-bucket/objects/images/c.txt", "C", "public")

	firstReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/list-bucket/objects?prefix=docs/&limit=1", nil)
	firstReq.Header.Set("Authorization", "Bearer dev-token")
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", firstRec.Code)
	}

	var firstBody apiEnvelope[objectListResponse]
	decodeJSON(t, firstRec.Body.Bytes(), &firstBody)
	if len(firstBody.Data.Items) != 1 || firstBody.Data.Items[0].ObjectKey != "docs/b.txt" {
		t.Fatalf("unexpected first page: %+v", firstBody.Data.Items)
	}
	if firstBody.Data.NextCursor == "" {
		t.Fatalf("expected next_cursor")
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/list-bucket/objects?prefix=docs/&limit=1&cursor="+url.QueryEscape(firstBody.Data.NextCursor), nil)
	secondReq.Header.Set("Authorization", "Bearer dev-token")
	secondRec := httptest.NewRecorder()
	router.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", secondRec.Code)
	}

	var secondBody apiEnvelope[objectListResponse]
	decodeJSON(t, secondRec.Body.Bytes(), &secondBody)
	if len(secondBody.Data.Items) != 1 || secondBody.Data.Items[0].ObjectKey != "docs/a.txt" {
		t.Fatalf("unexpected second page: %+v", secondBody.Data.Items)
	}
}

func TestUploadDecodesEncodedOriginalFilenameHeader(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "encoded-bucket")

	req := httptest.NewRequest(http.MethodPut, "/api/v1/buckets/encoded-bucket/objects/docs/report.txt", strings.NewReader("hello"))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-Object-Visibility", "public")
	req.Header.Set("X-Original-Filename", url.PathEscape("中文报告.txt"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var uploadBody apiEnvelope[objectResponse]
	decodeJSON(t, rec.Body.Bytes(), &uploadBody)
	if uploadBody.Data.OriginalFilename != "中文报告.txt" {
		t.Fatalf("unexpected original filename %q", uploadBody.Data.OriginalFilename)
	}
}

func TestDownloadObjectEncodesFilenameHeadersAndAddsUTF8Charset(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "download-bucket")

	req := httptest.NewRequest(http.MethodPut, "/api/v1/buckets/download-bucket/objects/docs/report.txt", strings.NewReader("我是 Light OSS"))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-Object-Visibility", "public")
	req.Header.Set("X-Original-Filename", url.PathEscape("中文报告.txt"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/download-bucket/objects/docs/report.txt", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRec.Code)
	}
	if body := getRec.Body.String(); body != "我是 Light OSS" {
		t.Fatalf("unexpected body %q", body)
	}
	if got := getRec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected content type %q", got)
	}
	if got := getRec.Header().Get("X-Original-Filename"); got != url.PathEscape("中文报告.txt") {
		t.Fatalf("unexpected encoded filename header %q", got)
	}
	contentDisposition := strings.ToLower(getRec.Header().Get("Content-Disposition"))
	if !strings.Contains(contentDisposition, "inline") {
		t.Fatalf("expected inline content disposition, got %q", contentDisposition)
	}
	if !strings.Contains(contentDisposition, "filename*=") || !strings.Contains(contentDisposition, "%e4%b8%ad%e6%96%87%e6%8a%a5%e5%91%8a.txt") {
		t.Fatalf("unexpected content disposition %q", contentDisposition)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/download-bucket/objects/docs/report.txt?download=true", nil)
	downloadRec := httptest.NewRecorder()
	router.ServeHTTP(downloadRec, downloadReq)
	if downloadRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", downloadRec.Code)
	}
	downloadDisposition := strings.ToLower(downloadRec.Header().Get("Content-Disposition"))
	if !strings.Contains(downloadDisposition, "attachment") {
		t.Fatalf("expected attachment content disposition, got %q", downloadDisposition)
	}
}

func TestDownloadObjectRejectsInvalidDownloadQuery(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "download-invalid-query-bucket")
	uploadObject(t, router, "/api/v1/buckets/download-invalid-query-bucket/objects/docs/report.txt", "hello", "public")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/download-invalid-query-bucket/objects/docs/report.txt?download=maybe", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
	}

	assertAPIErrorCode(t, rec.Body.Bytes(), "invalid_request")
}

func TestDownloadObjectPreservesExplicitCharset(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "charset-bucket")
	uploadObjectWithContentType(t, router, "/api/v1/buckets/charset-bucket/objects/docs/explicit.txt", "explicit", "public", "text/plain; charset=gbk")

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/charset-bucket/objects/docs/explicit.txt", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRec.Code)
	}
	if got := getRec.Header().Get("Content-Type"); got != "text/plain; charset=gbk" {
		t.Fatalf("unexpected content type %q", got)
	}
}

func TestUploadObjectBatchSuccess(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "batch-bucket")

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/batch-bucket/objects/batch",
		map[string]string{
			"prefix":     "docs/",
			"visibility": "public",
			"manifest": mustMarshalJSON(t, []map[string]string{
				{"file_field": "file_0", "relative_path": "assets/readme.txt"},
				{"file_field": "file_1", "relative_path": "assets/images/logo.png"},
			}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "readme.txt", Content: "hello world", ContentType: "text/plain"},
			"file_1": {Filename: "logo.png", Content: "png-bytes", ContentType: "image/png"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[uploadBatchResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.UploadedCount != 2 {
		t.Fatalf("expected uploaded_count 2, got %d", body.Data.UploadedCount)
	}
	if len(body.Data.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(body.Data.Items))
	}
	if body.Data.Items[0].ObjectKey != "docs/assets/readme.txt" {
		t.Fatalf("unexpected first object key %q", body.Data.Items[0].ObjectKey)
	}
	if body.Data.Items[1].ObjectKey != "docs/assets/images/logo.png" {
		t.Fatalf("unexpected second object key %q", body.Data.Items[1].ObjectKey)
	}
	if body.Data.Items[0].Visibility != "public" || body.Data.Items[1].Visibility != "public" {
		t.Fatalf("expected public visibility, got %+v", body.Data.Items)
	}
}

func TestUploadObjectBatchConflictRequiresAllowOverwriteHeader(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "overwrite-batch")
	uploadObject(t, router, "/api/v1/buckets/overwrite-batch/objects/docs/assets/readme.txt", "old", "public")

	fields := map[string]string{
		"prefix":     "docs/",
		"visibility": "public",
		"manifest": mustMarshalJSON(t, []map[string]string{
			{"file_field": "file_0", "relative_path": "assets/readme.txt"},
			{"file_field": "file_1", "relative_path": "assets/new.txt"},
		}),
	}
	files := map[string]multipartUploadFile{
		"file_0": {Filename: "readme.txt", Content: "new", ContentType: "text/plain"},
		"file_1": {Filename: "new.txt", Content: "new-file", ContentType: "text/plain"},
	}

	conflictReq := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/overwrite-batch/objects/batch",
		fields,
		files,
	)
	conflictRec := httptest.NewRecorder()
	router.ServeHTTP(conflictRec, conflictReq)

	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d, body=%s", conflictRec.Code, conflictRec.Body.String())
	}
	assertAPIErrorCode(t, conflictRec.Body.Bytes(), "object_exists")

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/overwrite-batch/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}
	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected only original object after conflict, got %d", len(listBody.Data.Items))
	}

	overwriteReq := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/overwrite-batch/objects/batch",
		fields,
		files,
	)
	overwriteReq.Header.Set("X-Allow-Overwrite", "true")
	overwriteRec := httptest.NewRecorder()
	router.ServeHTTP(overwriteRec, overwriteReq)
	if overwriteRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", overwriteRec.Code, overwriteRec.Body.String())
	}

	var uploadBody apiEnvelope[uploadBatchResponse]
	decodeJSON(t, overwriteRec.Body.Bytes(), &uploadBody)
	if uploadBody.Data.UploadedCount != 2 {
		t.Fatalf("expected uploaded_count 2, got %d", uploadBody.Data.UploadedCount)
	}
}

func TestUploadRejectsInvalidAllowOverwriteHeader(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "overwrite-invalid")

	objectReq := httptest.NewRequest(http.MethodPut, "/api/v1/buckets/overwrite-invalid/objects/docs/readme.txt", strings.NewReader("hello"))
	objectReq.Header.Set("Authorization", "Bearer dev-token")
	objectReq.Header.Set("X-Allow-Overwrite", "invalid")
	objectReq.Header.Set("X-Object-Visibility", "public")
	objectReq.Header.Set("X-Original-Filename", "readme.txt")
	objectReq.Header.Set("Content-Type", "text/plain")
	objectRec := httptest.NewRecorder()
	router.ServeHTTP(objectRec, objectReq)
	if objectRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", objectRec.Code, objectRec.Body.String())
	}
	assertAPIErrorCode(t, objectRec.Body.Bytes(), "invalid_request")

	batchReq := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/overwrite-invalid/objects/batch",
		map[string]string{
			"manifest": mustMarshalJSON(t, []map[string]string{{
				"file_field":    "file_0",
				"relative_path": "docs/readme.txt",
			}}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "readme.txt", Content: "hello", ContentType: "text/plain"},
		},
	)
	batchReq.Header.Set("X-Allow-Overwrite", "invalid")
	batchRec := httptest.NewRecorder()
	router.ServeHTTP(batchRec, batchReq)
	if batchRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", batchRec.Code, batchRec.Body.String())
	}
	assertAPIErrorCode(t, batchRec.Body.Bytes(), "invalid_request")
}

func TestUploadObjectBatchSupportsMoreThanThousandFiles(t *testing.T) {
	router := newTestRouter(t, 2*1024*1024)

	createBucket(t, router, "batch-many-files-bucket")

	manifest := make([]map[string]string, 0, 1001)
	files := make(map[string]multipartUploadFile, 1001)
	for i := 0; i < 1001; i++ {
		fieldName := fmt.Sprintf("file_%d", i)
		filename := fmt.Sprintf("asset-%d.txt", i)
		manifest = append(manifest, map[string]string{
			"file_field":    fieldName,
			"relative_path": "assets/" + filename,
		})
		files[fieldName] = multipartUploadFile{
			Filename: filename,
			Content:  "x",
		}
	}

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/buckets/batch-many-files-bucket/objects/batch",
		map[string]string{
			"manifest": mustMarshalJSON(t, manifest),
		},
		files,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[uploadBatchResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.UploadedCount != 1001 {
		t.Fatalf("expected 1001 uploaded files, got %d", body.Data.UploadedCount)
	}
}

func TestUploadObjectBatchValidationErrors(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "batch-validation-bucket")

	t.Run("invalid manifest json", func(t *testing.T) {
		req := newMultipartBatchUploadRequest(
			t,
			"/api/v1/buckets/batch-validation-bucket/objects/batch",
			map[string]string{
				"prefix":   "docs/",
				"manifest": "{",
			},
			map[string]multipartUploadFile{
				"file_0": {Filename: "readme.txt", Content: "hello"},
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
	})

	t.Run("missing file part", func(t *testing.T) {
		req := newMultipartBatchUploadRequest(
			t,
			"/api/v1/buckets/batch-validation-bucket/objects/batch",
			map[string]string{
				"manifest": mustMarshalJSON(t, []map[string]string{
					{"file_field": "missing", "relative_path": "assets/readme.txt"},
				}),
			},
			nil,
		)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
		}

		var body apiEnvelope[uploadBatchResponse]
		decodeJSON(t, rec.Body.Bytes(), &body)
		if body.Error == nil || body.Error.Code != "batch_file_missing" {
			t.Fatalf("expected batch_file_missing, got %+v", body.Error)
		}
	})
}
