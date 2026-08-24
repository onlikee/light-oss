package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
