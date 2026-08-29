package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
	"light-oss/backend/internal/service"
)

type recycleBinBatchRequest struct {
	ItemIDs []uint64 `json:"item_ids"`
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

type recycleBinRestoreResponse struct {
	RestoredCount int                            `json:"restored_count"`
	FailedCount   int                            `json:"failed_count"`
	FailedItems   []recycleBinFailedItemResponse `json:"failed_items"`
}

type recycleBinDeleteResponse struct {
	DeletedCount int                            `json:"deleted_count"`
	FailedCount  int                            `json:"failed_count"`
	FailedItems  []recycleBinFailedItemResponse `json:"failed_items"`
}

func (h *apiHandler) listRecycleBinObjects(c *gin.Context) {
	rawLimit, limitProvided := c.GetQuery("limit")
	limit, err := parseOptionalIntQuery(rawLimit, limitProvided)
	if err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "limit must be an integer"))
		return
	}
	if limitProvided && (limit < 1 || limit > 100) {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100"))
		return
	}
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

	response.JSON(c, http.StatusOK, recycleBinRestoreResponse{
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

	response.JSON(c, http.StatusOK, recycleBinDeleteResponse{
		DeletedCount: result.DeletedCount,
		FailedCount:  result.FailedCount,
		FailedItems:  failedItems,
	})
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
