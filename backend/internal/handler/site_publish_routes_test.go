package handler_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
