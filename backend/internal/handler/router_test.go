package handler_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"light-oss/backend/internal/config"
	"light-oss/backend/internal/handler"
	"light-oss/backend/internal/middleware"
	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/service"
	"light-oss/backend/internal/signing"
	"light-oss/backend/internal/storage"
)

type apiEnvelope[T any] struct {
	Data  T             `json:"data"`
	Error *apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type bucketResponse struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

type bucketListResponse struct {
	Items []bucketResponse `json:"items"`
}

type objectResponse struct {
	ObjectKey        string `json:"object_key"`
	OriginalFilename string `json:"original_filename"`
	Visibility       string `json:"visibility"`
	Size             int64  `json:"size"`
}

type objectListResponse struct {
	Items      []objectResponse `json:"items"`
	NextCursor string           `json:"next_cursor"`
}

type folderNodeResponse struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	ParentPath string `json:"parent_path"`
}

type folderListResponse struct {
	Items []folderNodeResponse `json:"items"`
}

type explorerEntryResponse struct {
	Type      string     `json:"type"`
	Path      string     `json:"path"`
	Name      string     `json:"name"`
	IsEmpty   *bool      `json:"is_empty"`
	ObjectKey *string    `json:"object_key"`
	CreatedAt *time.Time `json:"created_at"`
}

type explorerListResponse struct {
	Items      []explorerEntryResponse `json:"items"`
	NextCursor string                  `json:"next_cursor"`
}

type deleteExplorerEntriesBatchFailedItemResponse struct {
	Type    string `json:"type"`
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type deleteExplorerEntriesBatchResponse struct {
	DeletedCount int                                            `json:"deleted_count"`
	FailedCount  int                                            `json:"failed_count"`
	FailedItems  []deleteExplorerEntriesBatchFailedItemResponse `json:"failed_items"`
}

type recycleBinObjectResponse struct {
	ID         uint64 `json:"id"`
	Type       string `json:"type"`
	BucketName string `json:"bucket_name"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	ObjectKey  string `json:"object_key"`
	Size       int64  `json:"size"`
}

type recycleBinListResponse struct {
	Items      []recycleBinObjectResponse `json:"items"`
	NextCursor string                     `json:"next_cursor"`
}

type recycleBinFailedItemResponse struct {
	ID         uint64 `json:"id"`
	BucketName string `json:"bucket_name"`
	Path       string `json:"path"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

type recycleBinBatchResponse struct {
	DeletedCount  int                            `json:"deleted_count"`
	RestoredCount int                            `json:"restored_count"`
	FailedCount   int                            `json:"failed_count"`
	FailedItems   []recycleBinFailedItemResponse `json:"failed_items"`
}

type signResponse struct {
	URL string `json:"url"`
}

type siteResponse struct {
	ID            uint64   `json:"id"`
	Bucket        string   `json:"bucket"`
	RootPrefix    string   `json:"root_prefix"`
	Enabled       bool     `json:"enabled"`
	IndexDocument string   `json:"index_document"`
	ErrorDocument string   `json:"error_document"`
	SPAFallback   bool     `json:"spa_fallback"`
	Domains       []string `json:"domains"`
}

type siteListResponse struct {
	Items []siteResponse `json:"items"`
}

type uploadBatchResponse struct {
	UploadedCount int              `json:"uploaded_count"`
	Items         []objectResponse `json:"items"`
}

type publishSiteResponse struct {
	UploadedCount int          `json:"uploaded_count"`
	Site          siteResponse `json:"site"`
}

func TestProtectedRoutesRequireAuth(t *testing.T) {
	router := newTestRouter(t, 1024)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/buckets", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestCORSAllowsAllOrigins(t *testing.T) {
	router := newTestRouter(t, 1024)

	getReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	getReq.Header.Set("Origin", "http://console.example.com")
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRec.Code)
	}
	if got := getRec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard allow origin on GET, got %q", got)
	}

	optionsReq := httptest.NewRequest(http.MethodOptions, "/api/v1/buckets", nil)
	optionsReq.Header.Set("Origin", "http://console.example.com")
	optionsReq.Header.Set("Access-Control-Request-Method", http.MethodGet)
	optionsReq.Header.Set("Access-Control-Request-Headers", "Authorization")
	optionsRec := httptest.NewRecorder()
	router.ServeHTTP(optionsRec, optionsReq)

	if optionsRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", optionsRec.Code)
	}
	if got := optionsRec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard allow origin on preflight, got %q", got)
	}
	if got := optionsRec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Fatalf("expected Authorization in allow headers, got %q", got)
	}
}

func TestListBucketsSupportsSearch(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "alpha")
	createBucket(t, router, "beta")

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets?search="+url.QueryEscape("alp"),
		nil,
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[bucketListResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if len(body.Data.Items) != 1 || body.Data.Items[0].Name != "alpha" {
		t.Fatalf("expected only alpha bucket, got %+v", body.Data.Items)
	}
}

func TestListBucketsTreatsSearchWildcardsAsLiterals(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "alpha")
	createBucket(t, router, "beta")

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets?search="+url.QueryEscape("%"),
		nil,
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[bucketListResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if len(body.Data.Items) != 0 {
		t.Fatalf("expected wildcard search to return no buckets, got %+v", body.Data.Items)
	}
}

func TestProtectedHealthzRequiresAuthAndReturnsHealthState(t *testing.T) {
	router := newTestRouter(t, 1024)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
	unauthorizedRec := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedRec, unauthorizedReq)
	if unauthorizedRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorizedRec.Code)
	}

	authorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
	authorizedReq.Header.Set("Authorization", "Bearer dev-token")
	authorizedRec := httptest.NewRecorder()
	router.ServeHTTP(authorizedRec, authorizedReq)
	if authorizedRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", authorizedRec.Code, authorizedRec.Body.String())
	}

	var body apiEnvelope[map[string]any]
	decodeJSON(t, authorizedRec.Body.Bytes(), &body)

	status, ok := body.Data["status"].(map[string]any)
	if !ok {
		t.Fatalf("expected status object, got %+v", body.Data["status"])
	}
	if status["service"] != "ok" {
		t.Fatalf("expected service ok, got %+v", status["service"])
	}
	if status["db"] != "ok" {
		t.Fatalf("expected db ok, got %+v", status["db"])
	}
}

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
	parsed, err := url.Parse(signBody.Data.URL)
	if err != nil {
		t.Fatalf("parse signed url: %v", err)
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
	uploadObjectWithContentType(t, router, "/api/v1/buckets/charset-bucket/objects/docs/legacy.txt", "legacy", "public", "text/plain; charset=gbk")

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/charset-bucket/objects/docs/legacy.txt", nil)
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

func TestPublishSiteUploadSuccess(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "websites")

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/sites/publish",
		map[string]string{
			"bucket":        "websites",
			"parent_prefix": "deployments/",
			"domains": mustMarshalJSON(t, []string{
				"demo.localhost",
			}),
			"manifest": mustMarshalJSON(t, []map[string]string{
				{"file_field": "file_0", "relative_path": "dist/index.html"},
				{"file_field": "file_1", "relative_path": "dist/assets/app.js"},
			}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "index.html", Content: "<html>home</html>", ContentType: "text/html"},
			"file_1": {Filename: "app.js", Content: "console.log('demo')", ContentType: "application/javascript"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[publishSiteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.UploadedCount != 2 {
		t.Fatalf("expected uploaded_count 2, got %d", body.Data.UploadedCount)
	}
	if body.Data.Site.RootPrefix != "deployments/dist/" {
		t.Fatalf("expected root prefix deployments/dist/, got %q", body.Data.Site.RootPrefix)
	}
	if !body.Data.Site.Enabled {
		t.Fatalf("expected site enabled by default")
	}
	if !body.Data.Site.SPAFallback {
		t.Fatalf("expected spa fallback enabled by default")
	}
	if body.Data.Site.IndexDocument != "index.html" {
		t.Fatalf("expected default index document, got %q", body.Data.Site.IndexDocument)
	}

	indexReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sites/%d", body.Data.Site.ID), nil)
	indexRec := httptest.NewRecorder()
	router.ServeHTTP(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", indexRec.Code, indexRec.Body.String())
	}
	if indexRec.Body.String() != "<html>home</html>" {
		t.Fatalf("unexpected index body %q", indexRec.Body.String())
	}

	assetReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sites/%d/assets/app.js", body.Data.Site.ID), nil)
	assetRec := httptest.NewRecorder()
	router.ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", assetRec.Code, assetRec.Body.String())
	}
	if assetRec.Body.String() != "console.log('demo')" {
		t.Fatalf("unexpected asset body %q", assetRec.Body.String())
	}
}

func TestPublishSiteUploadOverwritesExistingObjects(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "websites")
	uploadObjectWithContentType(
		t,
		router,
		"/api/v1/buckets/websites/objects/deployments/dist/index.html",
		"<html>old</html>",
		"private",
		"text/html",
	)

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/sites/publish",
		map[string]string{
			"bucket":        "websites",
			"parent_prefix": "deployments/",
			"domains": mustMarshalJSON(t, []string{
				"overwrite.localhost",
			}),
			"manifest": mustMarshalJSON(t, []map[string]string{
				{"file_field": "file_0", "relative_path": "dist/index.html"},
			}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "index.html", Content: "<html>new</html>", ContentType: "text/html"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[publishSiteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.UploadedCount != 1 {
		t.Fatalf("expected uploaded_count 1, got %d", body.Data.UploadedCount)
	}
	if body.Data.Site.RootPrefix != "deployments/dist/" {
		t.Fatalf("expected root prefix deployments/dist/, got %q", body.Data.Site.RootPrefix)
	}

	indexReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sites/%d", body.Data.Site.ID), nil)
	indexRec := httptest.NewRecorder()
	router.ServeHTTP(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", indexRec.Code, indexRec.Body.String())
	}
	if indexRec.Body.String() != "<html>new</html>" {
		t.Fatalf("expected overwritten content, got %q", indexRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/websites/objects?prefix=deployments/dist/", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 object after overwrite, got %d", len(listBody.Data.Items))
	}
	if listBody.Data.Items[0].ObjectKey != "deployments/dist/index.html" {
		t.Fatalf("unexpected object key %q", listBody.Data.Items[0].ObjectKey)
	}
	if listBody.Data.Items[0].Visibility != "public" {
		t.Fatalf("expected public visibility after publish, got %q", listBody.Data.Items[0].Visibility)
	}
}

func TestPublishSiteFileSuccess(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "websites")

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/sites/publish/file",
		map[string]string{
			"bucket":        "websites",
			"parent_prefix": "deployments/",
			"domains": mustMarshalJSON(t, []string{
				"demo.localhost",
			}),
		},
		map[string]multipartUploadFile{
			"file": {Filename: "index.html", Content: "<html>home</html>", ContentType: "text/html"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[siteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.RootPrefix != "deployments/" {
		t.Fatalf("expected root prefix deployments/, got %q", body.Data.RootPrefix)
	}
	if body.Data.IndexDocument != "index.html" {
		t.Fatalf("expected index document index.html, got %q", body.Data.IndexDocument)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/websites/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 object, got %d", len(listBody.Data.Items))
	}
	if listBody.Data.Items[0].ObjectKey != "deployments/index.html" {
		t.Fatalf("unexpected object key %q", listBody.Data.Items[0].ObjectKey)
	}
	if listBody.Data.Items[0].Visibility != "public" {
		t.Fatalf("expected public visibility, got %q", listBody.Data.Items[0].Visibility)
	}

	indexReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sites/%d", body.Data.ID), nil)
	indexRec := httptest.NewRecorder()
	router.ServeHTTP(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", indexRec.Code, indexRec.Body.String())
	}
	if indexRec.Body.String() != "<html>home</html>" {
		t.Fatalf("unexpected index body %q", indexRec.Body.String())
	}
}

func TestPublishSiteFileSupportsBucketRoot(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "websites")

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/sites/publish/file",
		map[string]string{
			"bucket": "websites",
			"domains": mustMarshalJSON(t, []string{
				"root.localhost",
			}),
		},
		map[string]multipartUploadFile{
			"file": {Filename: "landing.txt", Content: "hello root", ContentType: "text/plain"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[siteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.RootPrefix != "" {
		t.Fatalf("expected empty root prefix, got %q", body.Data.RootPrefix)
	}
	if body.Data.IndexDocument != "landing.txt" {
		t.Fatalf("expected index document landing.txt, got %q", body.Data.IndexDocument)
	}

	indexReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sites/%d", body.Data.ID), nil)
	indexRec := httptest.NewRecorder()
	router.ServeHTTP(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", indexRec.Code, indexRec.Body.String())
	}
	if indexRec.Body.String() != "hello root" {
		t.Fatalf("unexpected index body %q", indexRec.Body.String())
	}
}

func TestPublishSiteFileRequiresFile(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "websites")

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/sites/publish/file",
		map[string]string{
			"bucket": "websites",
			"domains": mustMarshalJSON(t, []string{
				"demo.localhost",
			}),
		},
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[siteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "invalid_request" {
		t.Fatalf("expected invalid_request, got %+v", body.Error)
	}
}

func TestPublishSiteFileRollsBackStorageOnDomainConflict(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 8*1024)

	createBucket(t, router, "websites")
	createBucket(t, router, "other-sites")
	createSite(t, router, `{
		"bucket":"other-sites",
		"root_prefix":"existing/",
		"domains":["demo.localhost"]
	}`)

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/sites/publish/file",
		map[string]string{
			"bucket": "websites",
			"domains": mustMarshalJSON(t, []string{
				"demo.localhost",
			}),
		},
		map[string]multipartUploadFile{
			"file": {Filename: "index.html", Content: "<html>home</html>", ContentType: "text/html"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[siteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "domain_conflict" {
		t.Fatalf("expected domain_conflict, got %+v", body.Error)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/websites/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 0 {
		t.Fatalf("expected no persisted objects after conflict, got %+v", listBody.Data.Items)
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected no stored files after conflict, got %d", files)
	}
}

func TestPublishSiteUploadRejectsMixedTopLevelFolders(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 8*1024)

	createBucket(t, router, "websites")

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/sites/publish",
		map[string]string{
			"bucket": "websites",
			"domains": mustMarshalJSON(t, []string{
				"demo.localhost",
			}),
			"manifest": mustMarshalJSON(t, []map[string]string{
				{"file_field": "file_0", "relative_path": "dist/index.html"},
				{"file_field": "file_1", "relative_path": "app/assets/app.js"},
			}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "index.html", Content: "<html>home</html>", ContentType: "text/html"},
			"file_1": {Filename: "app.js", Content: "console.log('demo')", ContentType: "application/javascript"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[publishSiteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "invalid_batch_manifest" {
		t.Fatalf("expected invalid_batch_manifest, got %+v", body.Error)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/websites/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 0 {
		t.Fatalf("expected no persisted objects after invalid manifest, got %+v", listBody.Data.Items)
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected no stored files after invalid manifest, got %d", files)
	}
}

func TestPublishSiteUploadRollsBackObjectsOnDomainConflict(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 8*1024)

	createBucket(t, router, "websites")
	createBucket(t, router, "other-sites")
	createSite(t, router, `{
		"bucket":"other-sites",
		"root_prefix":"existing/",
		"domains":["demo.localhost"]
	}`)

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/sites/publish",
		map[string]string{
			"bucket": "websites",
			"domains": mustMarshalJSON(t, []string{
				"demo.localhost",
			}),
			"manifest": mustMarshalJSON(t, []map[string]string{
				{"file_field": "file_0", "relative_path": "dist/index.html"},
				{"file_field": "file_1", "relative_path": "dist/assets/app.js"},
			}),
		},
		map[string]multipartUploadFile{
			"file_0": {Filename: "index.html", Content: "<html>home</html>", ContentType: "text/html"},
			"file_1": {Filename: "app.js", Content: "console.log('demo')", ContentType: "application/javascript"},
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[publishSiteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "domain_conflict" {
		t.Fatalf("expected domain_conflict, got %+v", body.Error)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/websites/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 0 {
		t.Fatalf("expected no persisted objects after domain conflict, got %+v", listBody.Data.Items)
	}
	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected no stored files after domain conflict, got %d", files)
	}
}

func TestPublishObjectSiteSuccessFromPrivateFile(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "websites")
	uploadObjectWithContentType(
		t,
		router,
		"/api/v1/buckets/websites/objects/docs/landing.txt",
		"hello from object site",
		"private",
		"text/plain",
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sites/publish/object",
		bytes.NewBufferString(`{
			"bucket":"websites",
			"object_key":"docs/landing.txt",
			"domains":["demo.localhost"]
		}`),
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[siteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.RootPrefix != "docs/" {
		t.Fatalf("expected root prefix docs/, got %q", body.Data.RootPrefix)
	}
	if body.Data.IndexDocument != "landing.txt" {
		t.Fatalf("expected index document landing.txt, got %q", body.Data.IndexDocument)
	}
	if !body.Data.Enabled {
		t.Fatalf("expected site enabled by default")
	}
	if !body.Data.SPAFallback {
		t.Fatalf("expected spa fallback enabled by default")
	}

	siteReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sites/%d", body.Data.ID), nil)
	siteRec := httptest.NewRecorder()
	router.ServeHTTP(siteRec, siteReq)
	if siteRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", siteRec.Code, siteRec.Body.String())
	}
	if siteRec.Body.String() != "hello from object site" {
		t.Fatalf("unexpected site body %q", siteRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/websites/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 object, got %+v", listBody.Data.Items)
	}
	if listBody.Data.Items[0].Visibility != "public" {
		t.Fatalf("expected object visibility public, got %q", listBody.Data.Items[0].Visibility)
	}
}

func TestPublishObjectSiteSuccessFromRootFile(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "websites")
	uploadObjectWithContentType(
		t,
		router,
		"/api/v1/buckets/websites/objects/home.txt",
		"root file site",
		"public",
		"text/plain",
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sites/publish/object",
		bytes.NewBufferString(`{
			"bucket":"websites",
			"object_key":"home.txt",
			"domains":["root.localhost"],
			"enabled":false,
			"spa_fallback":false,
			"error_document":"404.txt"
		}`),
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[siteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.RootPrefix != "" {
		t.Fatalf("expected empty root prefix, got %q", body.Data.RootPrefix)
	}
	if body.Data.IndexDocument != "home.txt" {
		t.Fatalf("expected index document home.txt, got %q", body.Data.IndexDocument)
	}
	if body.Data.Enabled {
		t.Fatalf("expected site disabled")
	}
	if body.Data.SPAFallback {
		t.Fatalf("expected spa fallback disabled")
	}
	if body.Data.ErrorDocument != "404.txt" {
		t.Fatalf("expected error document 404.txt, got %q", body.Data.ErrorDocument)
	}
}

func TestPublishObjectSiteRollsBackVisibilityOnDomainConflict(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "websites")
	createBucket(t, router, "other-sites")
	uploadObjectWithContentType(
		t,
		router,
		"/api/v1/buckets/websites/objects/docs/landing.txt",
		"hello from object site",
		"private",
		"text/plain",
	)
	createSite(t, router, `{
		"bucket":"other-sites",
		"root_prefix":"existing/",
		"domains":["demo.localhost"]
	}`)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sites/publish/object",
		bytes.NewBufferString(`{
			"bucket":"websites",
			"object_key":"docs/landing.txt",
			"domains":["demo.localhost"]
		}`),
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[siteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "domain_conflict" {
		t.Fatalf("expected domain_conflict, got %+v", body.Error)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/websites/objects", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[objectListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 object, got %+v", listBody.Data.Items)
	}
	if listBody.Data.Items[0].Visibility != "private" {
		t.Fatalf("expected object visibility private after rollback, got %q", listBody.Data.Items[0].Visibility)
	}
}

func TestPublishObjectSiteReturnsObjectNotFound(t *testing.T) {
	router := newTestRouter(t, 8*1024)

	createBucket(t, router, "websites")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sites/publish/object",
		bytes.NewBufferString(`{
			"bucket":"websites",
			"object_key":"docs/missing.txt",
			"domains":["demo.localhost"]
		}`),
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[siteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "object_not_found" {
		t.Fatalf("expected object_not_found, got %+v", body.Error)
	}
}

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

func TestListFoldersAndExplorerEntries(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "tree-bucket")
	uploadObject(t, router, "/api/v1/buckets/tree-bucket/objects/docs/alpha.txt", "A", "public")
	uploadObject(t, router, "/api/v1/buckets/tree-bucket/objects/docs/zeta.txt", "Z", "public")
	uploadObject(t, router, "/api/v1/buckets/tree-bucket/objects/docs/images/c.txt", "C", "public")
	createFolder(t, router, "tree-bucket", "docs/", "empty")

	foldersReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/tree-bucket/folders", nil)
	foldersReq.Header.Set("Authorization", "Bearer dev-token")
	foldersRec := httptest.NewRecorder()
	router.ServeHTTP(foldersRec, foldersReq)
	if foldersRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", foldersRec.Code, foldersRec.Body.String())
	}

	var foldersBody apiEnvelope[folderListResponse]
	decodeJSON(t, foldersRec.Body.Bytes(), &foldersBody)
	if len(foldersBody.Data.Items) != 3 {
		t.Fatalf("unexpected folder count: %+v", foldersBody.Data.Items)
	}
	if foldersBody.Data.Items[0].Path != "docs/" || foldersBody.Data.Items[1].Path != "docs/empty/" || foldersBody.Data.Items[2].Path != "docs/images/" {
		t.Fatalf("unexpected folders: %+v", foldersBody.Data.Items)
	}

	firstEntriesReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/tree-bucket/entries?prefix=docs/&limit=2", nil)
	firstEntriesReq.Header.Set("Authorization", "Bearer dev-token")
	firstEntriesRec := httptest.NewRecorder()
	router.ServeHTTP(firstEntriesRec, firstEntriesReq)
	if firstEntriesRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", firstEntriesRec.Code, firstEntriesRec.Body.String())
	}

	var firstEntriesBody apiEnvelope[explorerListResponse]
	decodeJSON(t, firstEntriesRec.Body.Bytes(), &firstEntriesBody)
	if len(firstEntriesBody.Data.Items) != 2 {
		t.Fatalf("unexpected first entries page: %+v", firstEntriesBody.Data.Items)
	}
	if firstEntriesBody.Data.Items[0].Type != "directory" || firstEntriesBody.Data.Items[0].Name != "images" {
		t.Fatalf("unexpected first directory entry: %+v", firstEntriesBody.Data.Items[0])
	}
	if firstEntriesBody.Data.Items[0].IsEmpty == nil || *firstEntriesBody.Data.Items[0].IsEmpty {
		t.Fatalf("expected non-empty directory flag on %+v", firstEntriesBody.Data.Items[0])
	}
	if firstEntriesBody.Data.Items[0].CreatedAt != nil {
		t.Fatalf("expected directory created_at to be nil, got %+v", firstEntriesBody.Data.Items[0].CreatedAt)
	}
	if firstEntriesBody.Data.Items[1].Type != "directory" || firstEntriesBody.Data.Items[1].Name != "empty" {
		t.Fatalf("unexpected second directory entry: %+v", firstEntriesBody.Data.Items[1])
	}
	if firstEntriesBody.Data.Items[1].IsEmpty == nil || !*firstEntriesBody.Data.Items[1].IsEmpty {
		t.Fatalf("expected empty directory flag on %+v", firstEntriesBody.Data.Items[1])
	}
	if firstEntriesBody.Data.Items[1].CreatedAt != nil {
		t.Fatalf("expected directory created_at to be nil, got %+v", firstEntriesBody.Data.Items[1].CreatedAt)
	}
	if firstEntriesBody.Data.NextCursor == "" {
		t.Fatalf("expected next cursor for first entries page")
	}

	secondEntriesReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/tree-bucket/entries?prefix=docs/&limit=2&cursor="+url.QueryEscape(firstEntriesBody.Data.NextCursor),
		nil,
	)
	secondEntriesReq.Header.Set("Authorization", "Bearer dev-token")
	secondEntriesRec := httptest.NewRecorder()
	router.ServeHTTP(secondEntriesRec, secondEntriesReq)
	if secondEntriesRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", secondEntriesRec.Code, secondEntriesRec.Body.String())
	}

	var secondEntriesBody apiEnvelope[explorerListResponse]
	decodeJSON(t, secondEntriesRec.Body.Bytes(), &secondEntriesBody)
	if len(secondEntriesBody.Data.Items) != 2 {
		t.Fatalf("unexpected second entries page: %+v", secondEntriesBody.Data.Items)
	}
	if secondEntriesBody.Data.Items[0].Type != "file" || secondEntriesBody.Data.Items[0].Name != "zeta.txt" {
		t.Fatalf("unexpected file entry: %+v", secondEntriesBody.Data.Items[0])
	}
	if secondEntriesBody.Data.Items[0].CreatedAt == nil || secondEntriesBody.Data.Items[0].CreatedAt.IsZero() {
		t.Fatalf("expected file created_at on %+v", secondEntriesBody.Data.Items[0])
	}
	if secondEntriesBody.Data.Items[1].Type != "file" || secondEntriesBody.Data.Items[1].Name != "alpha.txt" {
		t.Fatalf("unexpected file entry: %+v", secondEntriesBody.Data.Items[1])
	}
	if secondEntriesBody.Data.Items[1].CreatedAt == nil || secondEntriesBody.Data.Items[1].CreatedAt.IsZero() {
		t.Fatalf("expected file created_at on %+v", secondEntriesBody.Data.Items[1])
	}

	searchReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/tree-bucket/entries?prefix=docs/&search=alp", nil)
	searchReq.Header.Set("Authorization", "Bearer dev-token")
	searchRec := httptest.NewRecorder()
	router.ServeHTTP(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", searchRec.Code, searchRec.Body.String())
	}

	var searchBody apiEnvelope[explorerListResponse]
	decodeJSON(t, searchRec.Body.Bytes(), &searchBody)
	if len(searchBody.Data.Items) != 1 || searchBody.Data.Items[0].Name != "alpha.txt" {
		t.Fatalf("unexpected search results: %+v", searchBody.Data.Items)
	}
}

func TestListExplorerEntriesSupportsSorting(t *testing.T) {
	router := newTestRouter(t, 1024)

	assertEntryNames := func(items []explorerEntryResponse, expected []string) {
		t.Helper()
		if len(items) != len(expected) {
			t.Fatalf("unexpected entry count: got %d want %d (%+v)", len(items), len(expected), items)
		}

		for index, item := range items {
			if item.Name != expected[index] {
				t.Fatalf("unexpected entries at index %d: got %+v want %s", index, items, expected[index])
			}
		}
	}

	createBucket(t, router, "sort-bucket")

	uploadObject(t, router, "/api/v1/buckets/sort-bucket/objects/docs/bravo.txt", "22", "public")
	createFolder(t, router, "sort-bucket", "docs/", "empty")
	time.Sleep(10 * time.Millisecond)
	uploadObject(t, router, "/api/v1/buckets/sort-bucket/objects/docs/delta.txt", "4444", "public")
	time.Sleep(10 * time.Millisecond)
	uploadObject(t, router, "/api/v1/buckets/sort-bucket/objects/docs/alpha.txt", "1", "public")
	time.Sleep(10 * time.Millisecond)
	uploadObject(t, router, "/api/v1/buckets/sort-bucket/objects/docs/charlie.txt", "33", "public")

	sizeAscReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/sort-bucket/entries?prefix=docs/&limit=3&sort_by=size&sort_order=asc",
		nil,
	)
	sizeAscReq.Header.Set("Authorization", "Bearer dev-token")
	sizeAscRec := httptest.NewRecorder()
	router.ServeHTTP(sizeAscRec, sizeAscReq)
	if sizeAscRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", sizeAscRec.Code, sizeAscRec.Body.String())
	}

	var sizeAscBody apiEnvelope[explorerListResponse]
	decodeJSON(t, sizeAscRec.Body.Bytes(), &sizeAscBody)
	assertEntryNames(sizeAscBody.Data.Items, []string{"empty", "alpha.txt", "bravo.txt"})
	if sizeAscBody.Data.Items[0].Type != "directory" {
		t.Fatalf("expected directory to stay first, got %+v", sizeAscBody.Data.Items[0])
	}
	if sizeAscBody.Data.NextCursor == "" {
		t.Fatalf("expected next cursor for size asc page")
	}

	sizeAscNextReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/sort-bucket/entries?prefix=docs/&limit=3&sort_by=size&sort_order=asc&cursor="+url.QueryEscape(sizeAscBody.Data.NextCursor),
		nil,
	)
	sizeAscNextReq.Header.Set("Authorization", "Bearer dev-token")
	sizeAscNextRec := httptest.NewRecorder()
	router.ServeHTTP(sizeAscNextRec, sizeAscNextReq)
	if sizeAscNextRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", sizeAscNextRec.Code, sizeAscNextRec.Body.String())
	}

	var sizeAscNextBody apiEnvelope[explorerListResponse]
	decodeJSON(t, sizeAscNextRec.Body.Bytes(), &sizeAscNextBody)
	assertEntryNames(sizeAscNextBody.Data.Items, []string{"charlie.txt", "delta.txt"})

	sizeDescReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/sort-bucket/entries?prefix=docs/&sort_by=size&sort_order=desc",
		nil,
	)
	sizeDescReq.Header.Set("Authorization", "Bearer dev-token")
	sizeDescRec := httptest.NewRecorder()
	router.ServeHTTP(sizeDescRec, sizeDescReq)
	if sizeDescRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", sizeDescRec.Code, sizeDescRec.Body.String())
	}

	var sizeDescBody apiEnvelope[explorerListResponse]
	decodeJSON(t, sizeDescRec.Body.Bytes(), &sizeDescBody)
	assertEntryNames(sizeDescBody.Data.Items, []string{"empty", "delta.txt", "charlie.txt", "bravo.txt", "alpha.txt"})

	createdAscReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/sort-bucket/entries?prefix=docs/&sort_by=created_at&sort_order=asc",
		nil,
	)
	createdAscReq.Header.Set("Authorization", "Bearer dev-token")
	createdAscRec := httptest.NewRecorder()
	router.ServeHTTP(createdAscRec, createdAscReq)
	if createdAscRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", createdAscRec.Code, createdAscRec.Body.String())
	}

	var createdAscBody apiEnvelope[explorerListResponse]
	decodeJSON(t, createdAscRec.Body.Bytes(), &createdAscBody)
	assertEntryNames(createdAscBody.Data.Items, []string{"empty", "bravo.txt", "delta.txt", "alpha.txt", "charlie.txt"})

	createdDescReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/sort-bucket/entries?prefix=docs/&sort_by=created_at&sort_order=desc",
		nil,
	)
	createdDescReq.Header.Set("Authorization", "Bearer dev-token")
	createdDescRec := httptest.NewRecorder()
	router.ServeHTTP(createdDescRec, createdDescReq)
	if createdDescRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", createdDescRec.Code, createdDescRec.Body.String())
	}

	var createdDescBody apiEnvelope[explorerListResponse]
	decodeJSON(t, createdDescRec.Body.Bytes(), &createdDescBody)
	assertEntryNames(createdDescBody.Data.Items, []string{"empty", "charlie.txt", "alpha.txt", "delta.txt", "bravo.txt"})

	invalidCursorReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/sort-bucket/entries?prefix=docs/&limit=3&sort_by=size&sort_order=desc&cursor="+url.QueryEscape(sizeAscBody.Data.NextCursor),
		nil,
	)
	invalidCursorReq.Header.Set("Authorization", "Bearer dev-token")
	invalidCursorRec := httptest.NewRecorder()
	router.ServeHTTP(invalidCursorRec, invalidCursorReq)
	if invalidCursorRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", invalidCursorRec.Code, invalidCursorRec.Body.String())
	}

	var invalidCursorBody apiEnvelope[struct{}]
	decodeJSON(t, invalidCursorRec.Body.Bytes(), &invalidCursorBody)
	if invalidCursorBody.Error == nil || invalidCursorBody.Error.Code != "invalid_cursor" {
		t.Fatalf("expected invalid_cursor, got %+v", invalidCursorBody.Error)
	}
}

func TestListExplorerEntriesDefaultsToCreatedAtDesc(t *testing.T) {
	router := newTestRouter(t, 1024)

	assertEntryNames := func(items []explorerEntryResponse, expected []string) {
		t.Helper()
		if len(items) != len(expected) {
			t.Fatalf("unexpected entry count: got %d want %d (%+v)", len(items), len(expected), items)
		}

		for index, item := range items {
			if item.Name != expected[index] {
				t.Fatalf("unexpected entries at index %d: got %+v want %s", index, items, expected[index])
			}
		}
	}

	createBucket(t, router, "default-sort-bucket")

	uploadObject(t, router, "/api/v1/buckets/default-sort-bucket/objects/docs/bravo.txt", "22", "public")
	createFolder(t, router, "default-sort-bucket", "docs/", "empty")
	time.Sleep(10 * time.Millisecond)
	uploadObject(t, router, "/api/v1/buckets/default-sort-bucket/objects/docs/delta.txt", "4444", "public")
	time.Sleep(10 * time.Millisecond)
	uploadObject(t, router, "/api/v1/buckets/default-sort-bucket/objects/docs/alpha.txt", "1", "public")
	time.Sleep(10 * time.Millisecond)
	uploadObject(t, router, "/api/v1/buckets/default-sort-bucket/objects/docs/charlie.txt", "33", "public")

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/default-sort-bucket/entries?prefix=docs/",
		nil,
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[explorerListResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	assertEntryNames(body.Data.Items, []string{"empty", "charlie.txt", "alpha.txt", "delta.txt", "bravo.txt"})
	if body.Data.Items[0].Type != "directory" {
		t.Fatalf("expected directory to stay first, got %+v", body.Data.Items[0])
	}
}

func TestCreateAndDeleteFolder(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "folder-bucket")
	createFolder(t, router, "folder-bucket", "", "empty")

	duplicateReq := httptest.NewRequest(http.MethodPost, "/api/v1/buckets/folder-bucket/folders", bytes.NewBufferString(`{"prefix":"","name":"empty"}`))
	duplicateReq.Header.Set("Authorization", "Bearer dev-token")
	duplicateReq.Header.Set("Content-Type", "application/json")
	duplicateRec := httptest.NewRecorder()
	router.ServeHTTP(duplicateRec, duplicateReq)
	if duplicateRec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d, body=%s", duplicateRec.Code, duplicateRec.Body.String())
	}

	deleteEmptyReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/folder-bucket/folders?path="+url.QueryEscape("empty/"), nil)
	deleteEmptyReq.Header.Set("Authorization", "Bearer dev-token")
	deleteEmptyRec := httptest.NewRecorder()
	router.ServeHTTP(deleteEmptyRec, deleteEmptyReq)
	if deleteEmptyRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteEmptyRec.Code, deleteEmptyRec.Body.String())
	}

	uploadObject(t, router, "/api/v1/buckets/folder-bucket/objects/docs/readme.txt", "hello", "public")
	uploadObject(t, router, "/api/v1/buckets/folder-bucket/objects/docs/nested/guide.txt", "nested", "private")

	deleteNonEmptyReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/folder-bucket/folders?path="+url.QueryEscape("docs/"), nil)
	deleteNonEmptyReq.Header.Set("Authorization", "Bearer dev-token")
	deleteNonEmptyRec := httptest.NewRecorder()
	router.ServeHTTP(deleteNonEmptyRec, deleteNonEmptyReq)
	if deleteNonEmptyRec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d, body=%s", deleteNonEmptyRec.Code, deleteNonEmptyRec.Body.String())
	}

	deleteRecursiveReq := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/buckets/folder-bucket/folders?path="+url.QueryEscape("docs/")+"&recursive=true",
		nil,
	)
	deleteRecursiveReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRecursiveRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRecursiveRec, deleteRecursiveReq)
	if deleteRecursiveRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteRecursiveRec.Code, deleteRecursiveRec.Body.String())
	}

	listEntriesReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/folder-bucket/entries", nil)
	listEntriesReq.Header.Set("Authorization", "Bearer dev-token")
	listEntriesRec := httptest.NewRecorder()
	router.ServeHTTP(listEntriesRec, listEntriesReq)
	if listEntriesRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listEntriesRec.Code, listEntriesRec.Body.String())
	}

	var listEntriesBody apiEnvelope[explorerListResponse]
	decodeJSON(t, listEntriesRec.Body.Bytes(), &listEntriesBody)
	if len(listEntriesBody.Data.Items) != 0 {
		t.Fatalf("expected empty root after recursive delete, got %+v", listEntriesBody.Data.Items)
	}

	deleteMissingReq := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/buckets/folder-bucket/folders?path="+url.QueryEscape("missing/")+"&recursive=true",
		nil,
	)
	deleteMissingReq.Header.Set("Authorization", "Bearer dev-token")
	deleteMissingRec := httptest.NewRecorder()
	router.ServeHTTP(deleteMissingRec, deleteMissingReq)
	if deleteMissingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", deleteMissingRec.Code, deleteMissingRec.Body.String())
	}
}

func TestDeleteExplorerEntriesBatch(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "batch-delete-bucket")
	uploadObject(t, router, "/api/v1/buckets/batch-delete-bucket/objects/docs/readme.txt", "hello", "public")
	uploadObject(t, router, "/api/v1/buckets/batch-delete-bucket/objects/docs/nested/guide.txt", "nested", "private")
	uploadObject(t, router, "/api/v1/buckets/batch-delete-bucket/objects/notes.txt", "notes", "public")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/buckets/batch-delete-bucket/entries/batch-delete",
		bytes.NewBufferString(`{"items":[{"type":"file","path":"notes.txt"},{"type":"directory","path":"docs/"}]}`),
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[deleteExplorerEntriesBatchResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.DeletedCount != 2 || body.Data.FailedCount != 0 || len(body.Data.FailedItems) != 0 {
		t.Fatalf("unexpected batch delete response: %+v", body.Data)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/batch-delete-bucket/entries", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[explorerListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 0 {
		t.Fatalf("expected empty root after batch delete, got %+v", listBody.Data.Items)
	}
}

func TestDeleteExplorerEntriesBatchDeletesDescendantsBeforeParents(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "batch-delete-overlap-bucket")
	uploadObject(t, router, "/api/v1/buckets/batch-delete-overlap-bucket/objects/docs/readme.txt", "hello", "public")
	uploadObject(t, router, "/api/v1/buckets/batch-delete-overlap-bucket/objects/docs/nested/guide.txt", "nested", "private")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/buckets/batch-delete-overlap-bucket/entries/batch-delete",
		bytes.NewBufferString(`{"items":[{"type":"directory","path":"docs/"},{"type":"directory","path":"docs/nested/"},{"type":"file","path":"docs/nested/guide.txt"}]}`),
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[deleteExplorerEntriesBatchResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.DeletedCount != 3 || body.Data.FailedCount != 0 || len(body.Data.FailedItems) != 0 {
		t.Fatalf("unexpected batch delete response: %+v", body.Data)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/batch-delete-overlap-bucket/entries", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	var listBody apiEnvelope[explorerListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 0 {
		t.Fatalf("expected empty root after overlapping batch delete, got %+v", listBody.Data.Items)
	}
}

func TestDeleteExplorerEntriesBatchReportsPartialFailures(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "batch-delete-partial-bucket")
	uploadObject(t, router, "/api/v1/buckets/batch-delete-partial-bucket/objects/keep.txt", "keep", "public")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/buckets/batch-delete-partial-bucket/entries/batch-delete",
		bytes.NewBufferString(`{"items":[{"type":"file","path":"missing.txt"},{"type":"file","path":"keep.txt"},{"type":"directory","path":"ghost/"}]}`),
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[deleteExplorerEntriesBatchResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.DeletedCount != 1 || body.Data.FailedCount != 2 {
		t.Fatalf("unexpected batch delete response: %+v", body.Data)
	}
	if len(body.Data.FailedItems) != 2 {
		t.Fatalf("expected 2 failed items, got %+v", body.Data.FailedItems)
	}
	if body.Data.FailedItems[0].Code != "object_not_found" || body.Data.FailedItems[0].Path != "missing.txt" {
		t.Fatalf("unexpected first failed item: %+v", body.Data.FailedItems[0])
	}
	if body.Data.FailedItems[1].Code != "folder_not_found" || body.Data.FailedItems[1].Path != "ghost/" {
		t.Fatalf("unexpected second failed item: %+v", body.Data.FailedItems[1])
	}
}

func TestDeleteExplorerEntriesBatchRejectsInvalidRequests(t *testing.T) {
	router := newTestRouter(t, 1024)
	createBucket(t, router, "batch-delete-invalid-bucket")

	testCases := []struct {
		name string
		body string
	}{
		{
			name: "empty items",
			body: `{"items":[]}`,
		},
		{
			name: "invalid type",
			body: `{"items":[{"type":"bucket","path":"docs/"}]}`,
		},
		{
			name: "directory missing trailing slash",
			body: `{"items":[{"type":"directory","path":"docs"}]}`,
		},
		{
			name: "file path ends with slash",
			body: `{"items":[{"type":"file","path":"docs/"}]}`,
		},
		{
			name: "too many items",
			body: `{"items":[` + strings.Repeat(`{"type":"file","path":"docs/readme.txt"},`, 200) + `{"type":"file","path":"docs/final.txt"}]}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/buckets/batch-delete-invalid-bucket/entries/batch-delete",
				bytes.NewBufferString(tc.body),
			)
			req.Header.Set("Authorization", "Bearer dev-token")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
			}

			assertAPIErrorCode(t, rec.Body.Bytes(), "invalid_request")
		})
	}
}

func TestDeleteExplorerEntriesBatchReturnsBucketNotFound(t *testing.T) {
	router := newTestRouter(t, 1024)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/buckets/missing-bucket/entries/batch-delete",
		bytes.NewBufferString(`{"items":[{"type":"file","path":"docs/readme.txt"}]}`),
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", rec.Code, rec.Body.String())
	}

	assertAPIErrorCode(t, rec.Body.Bytes(), "bucket_not_found")
}

func TestDeleteBucketCascadesAndCleansStorage(t *testing.T) {
	router, storageRoot := newTestRouterWithStorageRoot(t, 1024)

	createBucket(t, router, "wipe-bucket")
	uploadObject(t, router, "/api/v1/buckets/wipe-bucket/objects/docs/readme.txt", "hello", "public")
	createFolder(t, router, "wipe-bucket", "docs/", "empty")
	createSite(t, router, `{
		"bucket":"wipe-bucket",
		"root_prefix":"docs/",
		"domains":["demo.localhost"]
	}`)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/wipe-bucket", nil)
	deleteReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	listBucketsReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets", nil)
	listBucketsReq.Header.Set("Authorization", "Bearer dev-token")
	listBucketsRec := httptest.NewRecorder()
	router.ServeHTTP(listBucketsRec, listBucketsReq)
	if listBucketsRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listBucketsRec.Code, listBucketsRec.Body.String())
	}

	var bucketBody apiEnvelope[bucketListResponse]
	decodeJSON(t, listBucketsRec.Body.Bytes(), &bucketBody)
	if len(bucketBody.Data.Items) != 0 {
		t.Fatalf("expected no buckets after delete, got %+v", bucketBody.Data.Items)
	}

	listSitesReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites", nil)
	listSitesReq.Header.Set("Authorization", "Bearer dev-token")
	listSitesRec := httptest.NewRecorder()
	router.ServeHTTP(listSitesRec, listSitesReq)
	if listSitesRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listSitesRec.Code, listSitesRec.Body.String())
	}

	var siteBody apiEnvelope[siteListResponse]
	decodeJSON(t, listSitesRec.Body.Bytes(), &siteBody)
	if len(siteBody.Data.Items) != 0 {
		t.Fatalf("expected no sites after bucket delete, got %+v", siteBody.Data.Items)
	}

	if files := countFilesUnderRoot(t, storageRoot); files != 0 {
		t.Fatalf("expected no stored files after bucket delete, got %d", files)
	}
}

func TestDeletedBucketReadEndpointsReturnBucketNotFound(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "gone-bucket")
	uploadObject(t, router, "/api/v1/buckets/gone-bucket/objects/docs/readme.txt", "hello", "public")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/gone-bucket", nil)
	deleteReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	testCases := []string{
		"/api/v1/buckets/gone-bucket/objects",
		"/api/v1/buckets/gone-bucket/folders",
		"/api/v1/buckets/gone-bucket/entries",
		"/api/v1/buckets/gone-bucket/folders/archive?path=" + url.QueryEscape("docs/"),
	}

	for _, targetURL := range testCases {
		req := httptest.NewRequest(http.MethodGet, targetURL, nil)
		req.Header.Set("Authorization", "Bearer dev-token")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s expected 404, got %d, body=%s", targetURL, rec.Code, rec.Body.String())
		}

		assertAPIErrorCode(t, rec.Body.Bytes(), "bucket_not_found")
	}
}

func TestDeleteMissingBucketReturnsNotFound(t *testing.T) {
	router := newTestRouter(t, 1024)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/missing-bucket", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", rec.Code, rec.Body.String())
	}

	assertAPIErrorCode(t, rec.Body.Bytes(), "bucket_not_found")
}

func TestDownloadFolderArchive(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "archive-bucket")
	uploadObject(t, router, "/api/v1/buckets/archive-bucket/objects/docs/readme.txt", "hello", "public")
	uploadObject(t, router, "/api/v1/buckets/archive-bucket/objects/docs/nested/guide.txt", "nested", "private")
	createFolder(t, router, "archive-bucket", "docs/", "empty")

	unauthorizedReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/archive-bucket/folders/archive?path="+url.QueryEscape("docs/"),
		nil,
	)
	unauthorizedRec := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedRec, unauthorizedReq)
	if unauthorizedRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorizedRec.Code)
	}

	invalidReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/archive-bucket/folders/archive?path="+url.QueryEscape("docs"),
		nil,
	)
	invalidReq.Header.Set("Authorization", "Bearer dev-token")
	invalidRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", invalidRec.Code, invalidRec.Body.String())
	}

	missingReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/archive-bucket/folders/archive?path="+url.QueryEscape("missing/"),
		nil,
	)
	missingReq.Header.Set("Authorization", "Bearer dev-token")
	missingRec := httptest.NewRecorder()
	router.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", missingRec.Code, missingRec.Body.String())
	}

	downloadReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets/archive-bucket/folders/archive?path="+url.QueryEscape("docs/"),
		nil,
	)
	downloadReq.Header.Set("Authorization", "Bearer dev-token")
	downloadRec := httptest.NewRecorder()
	router.ServeHTTP(downloadRec, downloadReq)
	if downloadRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", downloadRec.Code, downloadRec.Body.String())
	}
	if got := downloadRec.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("expected application/zip, got %q", got)
	}
	if got := downloadRec.Header().Get("Content-Disposition"); !strings.Contains(got, "filename=docs.zip") {
		t.Fatalf("expected docs.zip content disposition, got %q", got)
	}

	entries := unzipEntries(t, downloadRec.Body.Bytes())
	if len(entries) != 4 {
		t.Fatalf("expected 4 zip entries, got %+v", entries)
	}
	if entries["docs/"] != "" {
		t.Fatalf("expected docs/ directory entry, got %q", entries["docs/"])
	}
	if entries["docs/empty/"] != "" {
		t.Fatalf("expected docs/empty/ directory entry, got %q", entries["docs/empty/"])
	}
	if entries["docs/readme.txt"] != "hello" {
		t.Fatalf("unexpected docs/readme.txt content %q", entries["docs/readme.txt"])
	}
	if entries["docs/nested/guide.txt"] != "nested" {
		t.Fatalf("unexpected docs/nested/guide.txt content %q", entries["docs/nested/guide.txt"])
	}
	if _, exists := entries["docs/.light-oss-folder"]; exists {
		t.Fatalf("folder marker should not be archived")
	}
}

func TestRecursiveDeleteEscapesLikeWildcards(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "wildcard-bucket")
	uploadObject(t, router, "/api/v1/buckets/wildcard-bucket/objects/a_/keep.txt", "keep", "public")
	uploadObject(t, router, "/api/v1/buckets/wildcard-bucket/objects/ab/stay.txt", "stay", "public")
	uploadObject(t, router, "/api/v1/buckets/wildcard-bucket/objects/ghosts/readme.txt", "ghost", "public")

	deleteUnderscoreReq := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/buckets/wildcard-bucket/folders?path="+url.QueryEscape("a_/")+"&recursive=true",
		nil,
	)
	deleteUnderscoreReq.Header.Set("Authorization", "Bearer dev-token")
	deleteUnderscoreRec := httptest.NewRecorder()
	router.ServeHTTP(deleteUnderscoreRec, deleteUnderscoreReq)
	if deleteUnderscoreRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteUnderscoreRec.Code, deleteUnderscoreRec.Body.String())
	}

	rootEntriesReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/wildcard-bucket/entries", nil)
	rootEntriesReq.Header.Set("Authorization", "Bearer dev-token")
	rootEntriesRec := httptest.NewRecorder()
	router.ServeHTTP(rootEntriesRec, rootEntriesReq)
	if rootEntriesRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rootEntriesRec.Code, rootEntriesRec.Body.String())
	}

	var rootEntriesBody apiEnvelope[explorerListResponse]
	decodeJSON(t, rootEntriesRec.Body.Bytes(), &rootEntriesBody)
	if len(rootEntriesBody.Data.Items) != 2 {
		t.Fatalf("expected 2 remaining root directories, got %+v", rootEntriesBody.Data.Items)
	}
	if rootEntriesBody.Data.Items[0].Path != "ghosts/" || rootEntriesBody.Data.Items[1].Path != "ab/" {
		t.Fatalf("unexpected remaining directories after underscore delete: %+v", rootEntriesBody.Data.Items)
	}

	deleteMissingWildcardReq := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/buckets/wildcard-bucket/folders?path="+url.QueryEscape("ghost%/")+"&recursive=true",
		nil,
	)
	deleteMissingWildcardReq.Header.Set("Authorization", "Bearer dev-token")
	deleteMissingWildcardRec := httptest.NewRecorder()
	router.ServeHTTP(deleteMissingWildcardRec, deleteMissingWildcardReq)
	if deleteMissingWildcardRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", deleteMissingWildcardRec.Code, deleteMissingWildcardRec.Body.String())
	}

	ghostEntriesReq := httptest.NewRequest(http.MethodGet, "/api/v1/buckets/wildcard-bucket/entries?prefix="+url.QueryEscape("ghosts/"), nil)
	ghostEntriesReq.Header.Set("Authorization", "Bearer dev-token")
	ghostEntriesRec := httptest.NewRecorder()
	router.ServeHTTP(ghostEntriesRec, ghostEntriesReq)
	if ghostEntriesRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", ghostEntriesRec.Code, ghostEntriesRec.Body.String())
	}

	var ghostEntriesBody apiEnvelope[explorerListResponse]
	decodeJSON(t, ghostEntriesRec.Body.Bytes(), &ghostEntriesBody)
	if len(ghostEntriesBody.Data.Items) != 1 || ghostEntriesBody.Data.Items[0].Path != "ghosts/readme.txt" {
		t.Fatalf("expected ghosts/readme.txt to remain after missing wildcard delete, got %+v", ghostEntriesBody.Data.Items)
	}
}

func TestUploadRejectsReservedFolderMarkerName(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "reserved-bucket")

	req := httptest.NewRequest(http.MethodPut, "/api/v1/buckets/reserved-bucket/objects/docs/.light-oss-folder", strings.NewReader("bad"))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-Object-Visibility", "private")
	req.Header.Set("X-Original-Filename", ".light-oss-folder")
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadSizeLimit(t *testing.T) {
	router := newTestRouter(t, 4)

	createBucket(t, router, "limit-bucket")

	req := httptest.NewRequest(http.MethodPut, "/api/v1/buckets/limit-bucket/objects/docs/oversized.txt", strings.NewReader("12345"))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-Object-Visibility", "public")
	req.Header.Set("X-Original-Filename", "oversized.txt")
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateObjectVisibility(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "visibility-bucket")
	uploadObject(t, router, "/api/v1/buckets/visibility-bucket/objects/docs/readme.txt", "hello", "private")

	unauthorizedReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/buckets/visibility-bucket/objects/visibility/docs/readme.txt",
		bytes.NewBufferString(`{"visibility":"public"}`),
	)
	unauthorizedReq.Header.Set("Content-Type", "application/json")
	unauthorizedRec := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedRec, unauthorizedReq)
	if unauthorizedRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorizedRec.Code)
	}

	invalidReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/buckets/visibility-bucket/objects/visibility/docs/readme.txt",
		bytes.NewBufferString(`{"visibility":"internal"}`),
	)
	invalidReq.Header.Set("Authorization", "Bearer dev-token")
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", invalidRec.Code, invalidRec.Body.String())
	}
	var invalidBody apiEnvelope[objectResponse]
	decodeJSON(t, invalidRec.Body.Bytes(), &invalidBody)
	if invalidBody.Error == nil || invalidBody.Error.Code != "invalid_visibility" {
		t.Fatalf("expected invalid_visibility error, got %+v", invalidBody.Error)
	}

	notFoundReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/buckets/visibility-bucket/objects/visibility/docs/missing.txt",
		bytes.NewBufferString(`{"visibility":"public"}`),
	)
	notFoundReq.Header.Set("Authorization", "Bearer dev-token")
	notFoundReq.Header.Set("Content-Type", "application/json")
	notFoundRec := httptest.NewRecorder()
	router.ServeHTTP(notFoundRec, notFoundReq)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", notFoundRec.Code, notFoundRec.Body.String())
	}

	updatePublicReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/buckets/visibility-bucket/objects/visibility/docs/readme.txt",
		bytes.NewBufferString(`{"visibility":"public"}`),
	)
	updatePublicReq.Header.Set("Authorization", "Bearer dev-token")
	updatePublicReq.Header.Set("Content-Type", "application/json")
	updatePublicRec := httptest.NewRecorder()
	router.ServeHTTP(updatePublicRec, updatePublicReq)
	if updatePublicRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", updatePublicRec.Code, updatePublicRec.Body.String())
	}
	var updatePublicBody apiEnvelope[objectResponse]
	decodeJSON(t, updatePublicRec.Body.Bytes(), &updatePublicBody)
	if updatePublicBody.Data.Visibility != "public" {
		t.Fatalf("expected visibility public, got %q", updatePublicBody.Data.Visibility)
	}

	updatePrivateReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/buckets/visibility-bucket/objects/visibility/docs/readme.txt",
		bytes.NewBufferString(`{"visibility":"private"}`),
	)
	updatePrivateReq.Header.Set("Authorization", "Bearer dev-token")
	updatePrivateReq.Header.Set("Content-Type", "application/json")
	updatePrivateRec := httptest.NewRecorder()
	router.ServeHTTP(updatePrivateRec, updatePrivateReq)
	if updatePrivateRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", updatePrivateRec.Code, updatePrivateRec.Body.String())
	}
	var updatePrivateBody apiEnvelope[objectResponse]
	decodeJSON(t, updatePrivateRec.Body.Bytes(), &updatePrivateBody)
	if updatePrivateBody.Data.Visibility != "private" {
		t.Fatalf("expected visibility private, got %q", updatePrivateBody.Data.Visibility)
	}
}

func TestSiteManagementCRUDAndDomainConflict(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "websites")
	createBucket(t, router, "other-sites")

	created := createSite(t, router, `{
		"bucket":"websites",
		"root_prefix":"demo",
		"domains":["demo.localhost"],
		"enabled":true
	}`)
	if created.RootPrefix != "demo/" {
		t.Fatalf("expected normalized root prefix, got %q", created.RootPrefix)
	}
	if created.IndexDocument != "index.html" {
		t.Fatalf("expected default index document, got %q", created.IndexDocument)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites", nil)
	listReq.Header.Set("Authorization", "Bearer dev-token")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}
	var listBody apiEnvelope[siteListResponse]
	decodeJSON(t, listRec.Body.Bytes(), &listBody)
	if len(listBody.Data.Items) != 1 {
		t.Fatalf("expected 1 site, got %d", len(listBody.Data.Items))
	}

	getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/sites/%d", created.ID), nil)
	getReq.Header.Set("Authorization", "Bearer dev-token")
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", getRec.Code, getRec.Body.String())
	}

	updateReq := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/v1/sites/%d", created.ID),
		bytes.NewBufferString(`{
			"bucket":"websites",
			"root_prefix":"demo/",
			"domains":["demo.localhost","www.localhost"],
			"enabled":false,
			"index_document":"home.html",
			"error_document":"404.html",
			"spa_fallback":true
		}`),
	)
	updateReq.Header.Set("Authorization", "Bearer dev-token")
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	router.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", updateRec.Code, updateRec.Body.String())
	}
	var updateBody apiEnvelope[siteResponse]
	decodeJSON(t, updateRec.Body.Bytes(), &updateBody)
	if updateBody.Data.Enabled {
		t.Fatalf("expected site to be disabled after update")
	}
	if len(updateBody.Data.Domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(updateBody.Data.Domains))
	}

	conflictReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sites",
		bytes.NewBufferString(`{
			"bucket":"other-sites",
			"root_prefix":"app/",
			"domains":["demo.localhost"]
		}`),
	)
	conflictReq.Header.Set("Authorization", "Bearer dev-token")
	conflictReq.Header.Set("Content-Type", "application/json")
	conflictRec := httptest.NewRecorder()
	router.ServeHTTP(conflictRec, conflictReq)
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d, body=%s", conflictRec.Code, conflictRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/sites/%d", created.ID), nil)
	deleteReq.Header.Set("Authorization", "Bearer dev-token")
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestSiteManagementAcceptsCustomHostnames(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "websites")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sites",
		bytes.NewBufferString(`{
			"bucket":"websites",
			"root_prefix":"demo/",
			"domains":["www.demo.example.com"]
		}`),
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSitePublicRoutesServeIndexAssetsAndHostMapping(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "websites")
	uploadObjectWithContentType(t, router, "/api/v1/buckets/websites/objects/demo/index.html", "<html>home</html>", "public", "text/html")
	uploadObjectWithContentType(t, router, "/api/v1/buckets/websites/objects/demo/assets/app.js", "console.log('demo')", "public", "application/javascript")
	uploadObjectWithContentType(t, router, "/api/v1/buckets/websites/objects/demo/docs/index.html", "<html>docs</html>", "public", "text/html")

	site := createSite(t, router, `{
		"bucket":"websites",
		"root_prefix":"demo/",
		"domains":["demo.localhost"]
	}`)

	indexReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sites/%d", site.ID), nil)
	indexRec := httptest.NewRecorder()
	router.ServeHTTP(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", indexRec.Code, indexRec.Body.String())
	}
	if body := indexRec.Body.String(); body != "<html>home</html>" {
		t.Fatalf("unexpected index body %q", body)
	}

	headReq := httptest.NewRequest(http.MethodHead, fmt.Sprintf("/sites/%d", site.ID), nil)
	headRec := httptest.NewRecorder()
	router.ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", headRec.Code)
	}
	if headRec.Body.Len() != 0 {
		t.Fatalf("expected empty body for HEAD, got %q", headRec.Body.String())
	}

	assetReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sites/%d/assets/app.js", site.ID), nil)
	assetRec := httptest.NewRecorder()
	router.ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", assetRec.Code, assetRec.Body.String())
	}
	if got := assetRec.Header().Get("Content-Type"); got != "application/javascript" {
		t.Fatalf("expected application/javascript, got %q", got)
	}

	dirReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sites/%d/docs/", site.ID), nil)
	dirRec := httptest.NewRecorder()
	router.ServeHTTP(dirRec, dirReq)
	if dirRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", dirRec.Code, dirRec.Body.String())
	}
	if body := dirRec.Body.String(); body != "<html>docs</html>" {
		t.Fatalf("unexpected directory body %q", body)
	}

	hostReq := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	hostReq.Host = "demo.localhost"
	hostRec := httptest.NewRecorder()
	router.ServeHTTP(hostRec, hostReq)
	if hostRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", hostRec.Code, hostRec.Body.String())
	}
	if body := hostRec.Body.String(); body != "console.log('demo')" {
		t.Fatalf("unexpected host-routed body %q", body)
	}
}

func TestSitePublicRoutesFallbackAndPrivateProtection(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "websites")
	uploadObjectWithContentType(t, router, "/api/v1/buckets/websites/objects/demo/index.html", "<html>app</html>", "public", "text/html")
	uploadObjectWithContentType(t, router, "/api/v1/buckets/websites/objects/demo/404.html", "<html>missing</html>", "public", "text/html")
	uploadObjectWithContentType(t, router, "/api/v1/buckets/websites/objects/demo/secret.txt", "hidden", "private", "text/plain")

	site := createSite(t, router, `{
		"bucket":"websites",
		"root_prefix":"demo/",
		"domains":["demo.localhost"],
		"spa_fallback":true
	}`)

	spaReq := httptest.NewRequest(http.MethodGet, "/dashboard/settings", nil)
	spaReq.Host = "demo.localhost"
	spaRec := httptest.NewRecorder()
	router.ServeHTTP(spaRec, spaReq)
	if spaRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", spaRec.Code, spaRec.Body.String())
	}
	if body := spaRec.Body.String(); body != "<html>app</html>" {
		t.Fatalf("unexpected spa fallback body %q", body)
	}

	privateReq := httptest.NewRequest(http.MethodGet, "/secret.txt", nil)
	privateReq.Host = "demo.localhost"
	privateRec := httptest.NewRecorder()
	router.ServeHTTP(privateRec, privateReq)
	if privateRec.Code != http.StatusOK {
		t.Fatalf("expected spa fallback to mask private object, got %d, body=%s", privateRec.Code, privateRec.Body.String())
	}
	if body := privateRec.Body.String(); body != "<html>app</html>" {
		t.Fatalf("unexpected body for private-object fallback %q", body)
	}

	updateReq := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/v1/sites/%d", site.ID),
		bytes.NewBufferString(`{
			"bucket":"websites",
			"root_prefix":"demo/",
			"domains":["demo.localhost"],
			"spa_fallback":false,
			"error_document":"404.html"
		}`),
	)
	updateReq.Header.Set("Authorization", "Bearer dev-token")
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	router.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", updateRec.Code, updateRec.Body.String())
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/missing/page", nil)
	missingReq.Host = "demo.localhost"
	missingRec := httptest.NewRecorder()
	router.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", missingRec.Code, missingRec.Body.String())
	}
	if body := missingRec.Body.String(); body != "<html>missing</html>" {
		t.Fatalf("unexpected error document body %q", body)
	}

	disabledReq := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/v1/sites/%d", site.ID),
		bytes.NewBufferString(`{
			"bucket":"websites",
			"root_prefix":"demo/",
			"domains":["demo.localhost"],
			"enabled":false
		}`),
	)
	disabledReq.Header.Set("Authorization", "Bearer dev-token")
	disabledReq.Header.Set("Content-Type", "application/json")
	disabledRec := httptest.NewRecorder()
	router.ServeHTTP(disabledRec, disabledReq)
	if disabledRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", disabledRec.Code, disabledRec.Body.String())
	}

	disabledSiteReq := httptest.NewRequest(http.MethodGet, "/anything", nil)
	disabledSiteReq.Host = "demo.localhost"
	disabledSiteRec := httptest.NewRecorder()
	router.ServeHTTP(disabledSiteRec, disabledSiteReq)
	if disabledSiteRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", disabledSiteRec.Code)
	}
}

func TestSiteNoRouteDoesNotConsumeAPIOrUnknownHosts(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "websites")
	createSite(t, router, `{
		"bucket":"websites",
		"root_prefix":"demo/",
		"domains":["demo.localhost"]
	}`)

	apiReq := httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil)
	apiReq.Host = "demo.localhost"
	apiRec := httptest.NewRecorder()
	router.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", apiRec.Code)
	}

	hostReq := httptest.NewRequest(http.MethodGet, "/", nil)
	hostReq.Host = "unknown.localhost"
	hostRec := httptest.NewRecorder()
	router.ServeHTTP(hostRec, hostReq)
	if hostRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", hostRec.Code)
	}
}

func newTestRouter(t *testing.T, maxUploadSize int64) *gin.Engine {
	router, _ := newTestRouterWithStorageRoot(t, maxUploadSize)
	return router
}

func newTestRouterWithStorageRoot(t *testing.T, maxUploadSize int64) (*gin.Engine, string) {
	router, root, _ := newTestRouterWithStorageRootAndDB(t, maxUploadSize)
	return router, root
}

func newTestRouterWithStorageRootAndDB(t *testing.T, maxUploadSize int64) (*gin.Engine, string, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatalf("enable sqlite foreign keys: %v", err)
	}

	if err := db.AutoMigrate(&model.Bucket{}, &model.SystemStorageQuota{}, &model.Object{}, &model.RecycleBinObject{}, &model.Site{}, &model.SiteDomain{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}

	root := t.TempDir()
	cfg := config.Config{
		AppEnv:                     "development",
		AppAddr:                    ":0",
		PublicBaseURL:              "http://example.com",
		StorageRoot:                filepath.ToSlash(root),
		MaxUploadSizeBytes:         maxUploadSize,
		MaxMultipartMemoryBytes:    8 * 1024 * 1024,
		RateLimitRPS:               1000,
		RateLimitBurst:             1000,
		BearerTokens:               []string{"dev-token"},
		SigningSecret:              "test-secret",
		DefaultSignedURLTTLSeconds: 300,
		MaxSignedURLTTLSeconds:     86400,
	}

	bucketRepo := repository.NewBucketRepository(db)
	objectRepo := repository.NewObjectRepository(db)
	recycleRepo := repository.NewRecycleBinRepository(db)
	siteRepo := repository.NewSiteRepository(db)
	localStorage := storage.NewLocalStorage(root)
	storageQuotaRepo := repository.NewStorageQuotaRepository(db)
	storageQuotaService := service.NewStorageQuotaService(zap.NewNop(), root, localStorage, objectRepo, recycleRepo, storageQuotaRepo)
	objectService := service.NewObjectService(db, bucketRepo, objectRepo, recycleRepo, localStorage, storageQuotaService)
	recycleBinService := service.NewRecycleBinService(db, bucketRepo, objectRepo, recycleRepo, storageQuotaService)
	siteService := service.NewSiteService(bucketRepo, siteRepo, objectService)
	return handler.NewRouter(handler.Dependencies{
		Config:              cfg,
		Logger:              zap.NewNop(),
		DB:                  sqlDB,
		GormDB:              db,
		AuthValidator:       middleware.NewTokenValidator(cfg.BearerTokens),
		BucketService:       service.NewBucketService(zap.NewNop(), db, bucketRepo, objectRepo, recycleRepo, siteRepo, storageQuotaService),
		ObjectService:       objectService,
		RecycleBinService:   recycleBinService,
		SiteService:         siteService,
		SitePublishService:  service.NewSitePublishService(db, objectRepo, siteRepo, localStorage, storageQuotaService, siteService),
		SignService:         service.NewSignService(signing.NewSigner(cfg.SigningSecret), cfg.PublicBaseURL, cfg.DefaultSignedURLTTLSeconds, cfg.MaxSignedURLTTLSeconds),
		SystemStatsService:  service.NewSystemStatsService(zap.NewNop(), storageQuotaService),
		StorageQuotaService: storageQuotaService,
	}), root, db
}

func createBucket(t *testing.T, router *gin.Engine, name string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/buckets", bytes.NewBufferString(`{"name":"`+name+`"}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bucket expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func uploadObject(t *testing.T, router *gin.Engine, path string, body string, visibility string) {
	t.Helper()
	uploadObjectWithContentType(t, router, path, body, visibility, "text/plain")
}

func uploadObjectWithContentType(t *testing.T, router *gin.Engine, path string, body string, visibility string, contentType string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-Object-Visibility", visibility)
	req.Header.Set("X-Original-Filename", "file.txt")
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func createSite(t *testing.T, router *gin.Engine, payload string) siteResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewBufferString(payload))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create site expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[siteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	return body.Data
}

func createFolder(t *testing.T, router *gin.Engine, bucket string, prefix string, name string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/buckets/"+bucket+"/folders", bytes.NewBufferString(`{"prefix":"`+prefix+`","name":"`+name+`"}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create folder expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func decodeJSON(t *testing.T, body []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode json: %v, body=%s", err, string(body))
	}
}

func assertAPIErrorCode(t *testing.T, body []byte, code string) {
	t.Helper()

	var envelope apiEnvelope[map[string]any]
	decodeJSON(t, body, &envelope)
	if envelope.Error == nil || envelope.Error.Code != code {
		t.Fatalf("expected error code %q, got %+v", code, envelope.Error)
	}
}

func unzipEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}

	entries := make(map[string]string, len(reader.File))
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			entries[file.Name] = ""
			continue
		}

		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", file.Name, err)
		}

		content, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", file.Name, err)
		}
		if closeErr != nil {
			t.Fatalf("close zip entry %s: %v", file.Name, closeErr)
		}

		entries[file.Name] = string(content)
	}

	return entries
}

type multipartUploadFile struct {
	Filename    string
	Content     string
	ContentType string
}

func newMultipartBatchUploadRequest(
	t *testing.T,
	targetURL string,
	fields map[string]string,
	files map[string]multipartUploadFile,
) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}

	for fieldName, file := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, file.Filename))
		if file.ContentType != "" {
			header.Set("Content-Type", file.ContentType)
		}

		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatalf("create file part %s: %v", fieldName, err)
		}
		if _, err := part.Write([]byte(file.Content)); err != nil {
			t.Fatalf("write file part %s: %v", fieldName, err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body.Bytes()))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	return string(raw)
}

func countFilesUnderRoot(t *testing.T, root string) int {
	t.Helper()

	count := 0
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		count++
		return nil
	}); err != nil {
		t.Fatalf("walk storage root: %v", err)
	}

	return count
}
