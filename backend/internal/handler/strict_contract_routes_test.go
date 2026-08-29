package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"light-oss/backend/internal/config"
)

func TestListRoutesRejectExplicitInvalidLimitsAndSorts(t *testing.T) {
	router := newTestRouter(t, 1024)
	createBucket(t, router, "contract-bucket")

	tests := []struct {
		name string
		url  string
	}{
		{name: "object non-integer limit", url: "/api/v1/buckets/contract-bucket/objects?limit=nope"},
		{name: "object empty limit", url: "/api/v1/buckets/contract-bucket/objects?limit="},
		{name: "object zero limit", url: "/api/v1/buckets/contract-bucket/objects?limit=0"},
		{name: "object excessive limit", url: "/api/v1/buckets/contract-bucket/objects?limit=101"},
		{name: "explorer zero limit", url: "/api/v1/buckets/contract-bucket/entries?limit=0"},
		{name: "explorer excessive limit", url: "/api/v1/buckets/contract-bucket/entries?limit=201"},
		{name: "explorer invalid sort by", url: "/api/v1/buckets/contract-bucket/entries?sort_by=created"},
		{name: "explorer uppercase sort order", url: "/api/v1/buckets/contract-bucket/entries?sort_order=DESC"},
		{name: "recycle bin zero limit", url: "/api/v1/recycle-bin/objects?limit=0"},
		{name: "recycle bin excessive limit", url: "/api/v1/recycle-bin/objects?limit=101"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			req.Header.Set("Authorization", "Bearer dev-token")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
			}
			var body apiEnvelope[any]
			decodeJSON(t, rec.Body.Bytes(), &body)
			if body.Error == nil || body.Error.Code != "invalid_request" {
				t.Fatalf("expected invalid_request, got %+v", body.Error)
			}
		})
	}
}

func TestBooleanInputsAcceptOnlyTrueOrFalse(t *testing.T) {
	router := newTestRouter(t, 8*1024)
	createBucket(t, router, "boolean-bucket")
	uploadObject(t, router, "/api/v1/buckets/boolean-bucket/objects/file.txt", "hello", "public")

	tests := []struct {
		name    string
		method  string
		url     string
		headers map[string]string
	}{
		{
			name:   "download query",
			method: http.MethodGet,
			url:    "/api/v1/buckets/boolean-bucket/objects/file.txt?download=1",
		},
		{
			name:   "recursive query",
			method: http.MethodDelete,
			url:    "/api/v1/buckets/boolean-bucket/folders?path=docs%2F&recursive=1",
		},
		{
			name:   "allow overwrite header",
			method: http.MethodPut,
			url:    "/api/v1/buckets/boolean-bucket/objects/other.txt",
			headers: map[string]string{
				"Content-Type":        "text/plain",
				"X-Allow-Overwrite":   "1",
				"X-Object-Visibility": "private",
				"X-Original-Filename": "other.txt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.url, strings.NewReader("content"))
			req.Header.Set("Authorization", "Bearer dev-token")
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d, body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	publishReq := newMultipartBatchUploadRequest(
		t,
		"/api/v1/sites/publish/file",
		map[string]string{
			"bucket":  "boolean-bucket",
			"domains": mustMarshalJSON(t, []string{"boolean.localhost"}),
			"enabled": "TRUE",
		},
		map[string]multipartUploadFile{
			"file": {Filename: "index.html", Content: "hello", ContentType: "text/html"},
		},
	)
	publishRec := httptest.NewRecorder()
	router.ServeHTTP(publishRec, publishReq)
	if publishRec.Code != http.StatusBadRequest {
		t.Fatalf("uppercase multipart boolean: expected 400, got %d, body=%s", publishRec.Code, publishRec.Body.String())
	}
}

func TestUpdateVisibilityRequiresExplicitValue(t *testing.T) {
	router := newTestRouter(t, 1024)
	createBucket(t, router, "visibility-required")
	uploadObject(t, router, "/api/v1/buckets/visibility-required/objects/file.txt", "hello", "private")

	for _, body := range []string{`{}`, `{"visibility":null}`} {
		req := httptest.NewRequest(
			http.MethodPatch,
			"/api/v1/buckets/visibility-required/objects/visibility/file.txt",
			bytes.NewBufferString(body),
		)
		req.Header.Set("Authorization", "Bearer dev-token")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: expected 400, got %d, response=%s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestUploadVisibilityDefaultsToPrivateAndUsesLowercaseEnum(t *testing.T) {
	router := newTestRouter(t, 1024)
	createBucket(t, router, "upload-visibility")

	defaultReq := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/buckets/upload-visibility/objects/default.txt",
		strings.NewReader("hello"),
	)
	defaultReq.Header.Set("Authorization", "Bearer dev-token")
	defaultReq.Header.Set("Content-Type", "text/plain")
	defaultReq.Header.Set("X-Original-Filename", "default.txt")
	defaultRec := httptest.NewRecorder()
	router.ServeHTTP(defaultRec, defaultReq)
	if defaultRec.Code != http.StatusCreated {
		t.Fatalf("omitted visibility: expected 201, got %d, body=%s", defaultRec.Code, defaultRec.Body.String())
	}
	var defaultBody apiEnvelope[objectResponse]
	decodeJSON(t, defaultRec.Body.Bytes(), &defaultBody)
	if defaultBody.Data.Visibility != "private" {
		t.Fatalf("omitted visibility default = %q, want private", defaultBody.Data.Visibility)
	}

	uppercaseReq := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/buckets/upload-visibility/objects/uppercase.txt",
		strings.NewReader("hello"),
	)
	uppercaseReq.Header.Set("Authorization", "Bearer dev-token")
	uppercaseReq.Header.Set("Content-Type", "text/plain")
	uppercaseReq.Header.Set("X-Original-Filename", "uppercase.txt")
	uppercaseReq.Header.Set("X-Object-Visibility", "PUBLIC")
	uppercaseRec := httptest.NewRecorder()
	router.ServeHTTP(uppercaseRec, uppercaseReq)
	if uppercaseRec.Code != http.StatusBadRequest {
		t.Fatalf("uppercase visibility: expected 400, got %d, body=%s", uppercaseRec.Code, uppercaseRec.Body.String())
	}
}

func TestCreateFolderDefaultsPrefixToBucketRoot(t *testing.T) {
	router := newTestRouter(t, 1024)
	createBucket(t, router, "root-folder")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/buckets/root-folder/folders",
		bytes.NewBufferString(`{"name":"docs"}`),
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}
	var body apiEnvelope[folderNodeResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Data.Path != "docs/" || body.Data.ParentPath != "" {
		t.Fatalf("unexpected root folder response: %+v", body.Data)
	}
}

func TestSignDownloadTTLDefaultsAndRejectsInvalidExplicitValues(t *testing.T) {
	router := newTestRouterWithConfig(t, 1024, func(cfg *config.Config) {
		cfg.DefaultSignedURLTTLSeconds = 5
		cfg.MaxSignedURLTTLSeconds = 10
	})

	startedAt := time.Now().UTC().Unix()
	defaultReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sign/download",
		bytes.NewBufferString(`{"bucket":"signed-bucket","object_key":"file.txt"}`),
	)
	defaultReq.Header.Set("Authorization", "Bearer dev-token")
	defaultReq.Header.Set("Content-Type", "application/json")
	defaultRec := httptest.NewRecorder()
	router.ServeHTTP(defaultRec, defaultReq)
	if defaultRec.Code != http.StatusOK {
		t.Fatalf("omitted TTL: expected 200, got %d, body=%s", defaultRec.Code, defaultRec.Body.String())
	}
	var defaultBody apiEnvelope[struct {
		ExpiresAt int64 `json:"expires_at"`
	}]
	decodeJSON(t, defaultRec.Body.Bytes(), &defaultBody)
	if defaultBody.Data.ExpiresAt < startedAt+4 || defaultBody.Data.ExpiresAt > startedAt+6 {
		t.Fatalf("omitted TTL did not use configured default: expires_at=%d started_at=%d", defaultBody.Data.ExpiresAt, startedAt)
	}

	for _, ttl := range []string{"0", "-1", "11"} {
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/sign/download",
			bytes.NewBufferString(`{"bucket":"signed-bucket","object_key":"file.txt","expires_in_seconds":`+ttl+`}`),
		)
		req.Header.Set("Authorization", "Bearer dev-token")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("TTL %s: expected 400, got %d, body=%s", ttl, rec.Code, rec.Body.String())
		}
		var body apiEnvelope[any]
		decodeJSON(t, rec.Body.Bytes(), &body)
		if body.Error == nil || body.Error.Code != "invalid_expiry" {
			t.Fatalf("TTL %s: expected invalid_expiry, got %+v", ttl, body.Error)
		}
	}
}

func TestPublishSiteFileReturnsPayloadTooLarge(t *testing.T) {
	router := newTestRouter(t, 4)
	createBucket(t, router, "oversized-site")

	req := newMultipartBatchUploadRequest(
		t,
		"/api/v1/sites/publish/file",
		map[string]string{
			"bucket":  "oversized-site",
			"domains": mustMarshalJSON(t, []string{"oversized.localhost"}),
		},
		map[string]multipartUploadFile{
			"file": {Filename: "index.html", Content: "too-large", ContentType: "text/html"},
		},
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d, body=%s", rec.Code, rec.Body.String())
	}
	var body apiEnvelope[any]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "payload_too_large" {
		t.Fatalf("expected payload_too_large, got %+v", body.Error)
	}
}
