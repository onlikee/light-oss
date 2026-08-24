package handler_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
