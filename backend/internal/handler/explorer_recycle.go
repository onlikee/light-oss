package handler

import (
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
	"light-oss/backend/internal/service"
)

type createFolderRequest struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
}

type deleteExplorerEntriesBatchRequest struct {
	Items []deleteExplorerEntriesBatchItemRequest `json:"items"`
}

type deleteExplorerEntriesBatchItemRequest struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type recycleBinBatchRequest struct {
	ItemIDs []uint64 `json:"item_ids"`
}

type folderNodeResponse struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	ParentPath string `json:"parent_path"`
}

type explorerEntryResponse struct {
	Type             string     `json:"type"`
	Path             string     `json:"path"`
	Name             string     `json:"name"`
	IsEmpty          *bool      `json:"is_empty"`
	ObjectKey        *string    `json:"object_key"`
	OriginalFilename *string    `json:"original_filename"`
	Size             *int64     `json:"size"`
	ContentType      *string    `json:"content_type"`
	ETag             *string    `json:"etag"`
	Visibility       *string    `json:"visibility"`
	CreatedAt        *time.Time `json:"created_at"`
	UpdatedAt        *time.Time `json:"updated_at"`
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
	ID               uint64    `json:"id"`
	Type             string    `json:"type"`
	BucketName       string    `json:"bucket_name"`
	Path             string    `json:"path"`
	Name             string    `json:"name"`
	ObjectKey        string    `json:"object_key"`
	OriginalFilename string    `json:"original_filename"`
	Size             int64     `json:"size"`
	ContentType      string    `json:"content_type"`
	ETag             string    `json:"etag"`
	Visibility       string    `json:"visibility"`
	CreatedAt        time.Time `json:"created_at"`
	DeletedAt        time.Time `json:"deleted_at"`
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

func (h *apiHandler) listFolders(c *gin.Context) {
	items, err := h.objectService.ListFolders(c.Request.Context(), c.Param("bucket"))
	if err != nil {
		response.Error(c, err)
		return
	}

	result := make([]folderNodeResponse, 0, len(items))
	for _, item := range items {
		result = append(result, folderNodeToResponse(item))
	}

	response.JSON(c, http.StatusOK, gin.H{"items": result})
}

func (h *apiHandler) createFolder(c *gin.Context) {
	var req createFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}

	folder, err := h.objectService.CreateFolder(c.Request.Context(), service.CreateFolderInput{
		BucketName: c.Param("bucket"),
		Prefix:     req.Prefix,
		Name:       req.Name,
	})
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusCreated, folderNodeToResponse(*folder))
}

func (h *apiHandler) deleteFolder(c *gin.Context) {
	recursive, err := parseOptionalBoolQuery(c.Query("recursive"))
	if err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "recursive query is invalid"))
		return
	}

	if err := h.objectService.DeleteFolder(c.Request.Context(), c.Param("bucket"), c.Query("path"), recursive); err != nil {
		response.Error(c, err)
		return
	}

	response.NoContent(c, http.StatusNoContent)
}

func (h *apiHandler) downloadFolderArchive(c *gin.Context) {
	archive, err := h.objectService.OpenFolderArchive(c.Request.Context(), c.Param("bucket"), c.Query("path"))
	if err != nil {
		response.Error(c, err)
		return
	}

	setFolderArchiveHeaders(c, archive.Filename)
	c.Status(http.StatusOK)

	if err := archive.StreamTo(c.Writer); err != nil {
		h.logger.Error("stream folder archive", zap.Error(err))
	}
}

func (h *apiHandler) listExplorerEntries(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	result, err := h.objectService.ListExplorerEntries(c.Request.Context(), service.ListExplorerEntriesInput{
		BucketName: c.Param("bucket"),
		Prefix:     c.Query("prefix"),
		Search:     c.Query("search"),
		Limit:      limit,
		Cursor:     c.Query("cursor"),
		SortBy:     c.Query("sort_by"),
		SortOrder:  c.Query("sort_order"),
	})
	if err != nil {
		response.Error(c, err)
		return
	}

	items := make([]explorerEntryResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, explorerEntryToResponse(item))
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items":       items,
		"next_cursor": result.NextCursor,
	})
}

func (h *apiHandler) deleteExplorerEntriesBatch(c *gin.Context) {
	var req deleteExplorerEntriesBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}

	items := make([]service.DeleteExplorerEntriesBatchItemInput, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, service.DeleteExplorerEntriesBatchItemInput{
			Type: item.Type,
			Path: item.Path,
		})
	}

	result, err := h.objectService.DeleteExplorerEntriesBatch(
		c.Request.Context(),
		c.Param("bucket"),
		items,
	)
	if err != nil {
		response.Error(c, err)
		return
	}

	failedItems := make([]deleteExplorerEntriesBatchFailedItemResponse, 0, len(result.FailedItems))
	for _, item := range result.FailedItems {
		failedItems = append(failedItems, deleteExplorerEntriesBatchFailedItemResponse{
			Type:    item.Type,
			Path:    item.Path,
			Code:    item.Code,
			Message: item.Message,
		})
	}

	response.JSON(c, http.StatusOK, deleteExplorerEntriesBatchResponse{
		DeletedCount: result.DeletedCount,
		FailedCount:  result.FailedCount,
		FailedItems:  failedItems,
	})
}

func (h *apiHandler) listRecycleBinObjects(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	result, err := h.recycleBinService.ListObjects(c.Request.Context(), service.ListRecycleBinObjectsInput{
		BucketName: c.Query("bucket"),
		Limit:      limit,
		Cursor:     c.Query("cursor"),
	})
	if err != nil {
		response.Error(c, err)
		return
	}

	items := make([]recycleBinObjectResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, recycleBinObjectToResponse(item))
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items":       items,
		"next_cursor": result.NextCursor,
	})
}

func (h *apiHandler) restoreRecycleBinObjects(c *gin.Context) {
	var req recycleBinBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}

	result, err := h.recycleBinService.RestoreObjects(c.Request.Context(), req.ItemIDs)
	if err != nil {
		response.Error(c, err)
		return
	}

	failedItems := make([]recycleBinFailedItemResponse, 0, len(result.FailedItems))
	for _, item := range result.FailedItems {
		failedItems = append(failedItems, recycleBinFailedItemResponse{
			ID:         item.ID,
			BucketName: item.BucketName,
			Path:       item.Path,
			Code:       item.Code,
			Message:    item.Message,
		})
	}

	response.JSON(c, http.StatusOK, recycleBinBatchResponse{
		RestoredCount: result.RestoredCount,
		FailedCount:   result.FailedCount,
		FailedItems:   failedItems,
	})
}

func (h *apiHandler) deleteRecycleBinObjects(c *gin.Context) {
	var req recycleBinBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}

	result, err := h.recycleBinService.DeleteObjects(c.Request.Context(), req.ItemIDs)
	if err != nil {
		response.Error(c, err)
		return
	}

	failedItems := make([]recycleBinFailedItemResponse, 0, len(result.FailedItems))
	for _, item := range result.FailedItems {
		failedItems = append(failedItems, recycleBinFailedItemResponse{
			ID:         item.ID,
			BucketName: item.BucketName,
			Path:       item.Path,
			Code:       item.Code,
			Message:    item.Message,
		})
	}

	response.JSON(c, http.StatusOK, recycleBinBatchResponse{
		DeletedCount: result.DeletedCount,
		FailedCount:  result.FailedCount,
		FailedItems:  failedItems,
	})
}

func folderNodeToResponse(node service.FolderNode) folderNodeResponse {
	return folderNodeResponse{
		Path:       node.Path,
		Name:       node.Name,
		ParentPath: node.ParentPath,
	}
}

func explorerEntryToResponse(entry service.ExplorerEntry) explorerEntryResponse {
	response := explorerEntryResponse{
		Type: string(entry.Type),
		Path: entry.Path,
		Name: entry.Name,
	}

	if entry.Type == service.ExplorerEntryTypeDirectory {
		isEmpty := entry.IsEmpty
		response.IsEmpty = &isEmpty
		return response
	}

	if entry.Object == nil {
		return response
	}

	objectKey := entry.Object.ObjectKey
	originalFilename := entry.Object.OriginalFilename
	size := entry.Object.Size
	contentType := entry.Object.ContentType
	etag := entry.Object.ETag
	visibility := string(entry.Object.Visibility)
	createdAt := entry.Object.CreatedAt
	updatedAt := entry.Object.UpdatedAt

	response.ObjectKey = &objectKey
	response.OriginalFilename = &originalFilename
	response.Size = &size
	response.ContentType = &contentType
	response.ETag = &etag
	response.Visibility = &visibility
	response.CreatedAt = &createdAt
	response.UpdatedAt = &updatedAt

	return response
}

func recycleBinObjectToResponse(item service.RecycleBinObjectItem) recycleBinObjectResponse {
	return recycleBinObjectResponse{
		ID:               item.ID,
		Type:             string(item.Type),
		BucketName:       item.BucketName,
		Path:             item.Path,
		Name:             item.Name,
		ObjectKey:        item.ObjectKey,
		OriginalFilename: item.OriginalFilename,
		Size:             item.Size,
		ContentType:      item.ContentType,
		ETag:             item.ETag,
		Visibility:       string(item.Visibility),
		CreatedAt:        item.CreatedAt,
		DeletedAt:        item.DeletedAt,
	}
}

func setFolderArchiveHeaders(c *gin.Context, filename string) {
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": filename,
	}))
}
