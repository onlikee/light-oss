package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

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
