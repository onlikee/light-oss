package handler_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

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
