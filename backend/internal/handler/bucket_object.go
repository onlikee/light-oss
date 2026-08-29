package handler

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
	"light-oss/backend/internal/service"
)

type createBucketRequest struct {
	Name string `json:"name"`
}

type updateObjectVisibilityRequest struct {
	Visibility *string `json:"visibility"`
}

type signDownloadRequest struct {
	Bucket           string `json:"bucket"`
	ObjectKey        string `json:"object_key"`
	ExpiresInSeconds *int64 `json:"expires_in_seconds"`
}

type bucketResponse struct {
	ID        uint64    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type objectResponse struct {
	ID               uint64    `json:"id"`
	BucketName       string    `json:"bucket_name"`
	ObjectKey        string    `json:"object_key"`
	OriginalFilename string    `json:"original_filename"`
	Size             int64     `json:"size"`
	ContentType      string    `json:"content_type"`
	ETag             string    `json:"etag"`
	Visibility       string    `json:"visibility"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (h *apiHandler) createBucket(c *gin.Context) {
	var req createBucketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}

	bucket, err := h.bucketService.Create(c.Request.Context(), req.Name)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusCreated, bucketToResponse(*bucket))
}

func (h *apiHandler) listBuckets(c *gin.Context) {
	buckets, err := h.bucketService.List(c.Request.Context(), c.Query("search"))
	if err != nil {
		response.Error(c, err)
		return
	}

	items := make([]bucketResponse, 0, len(buckets))
	for _, bucket := range buckets {
		items = append(items, bucketToResponse(bucket))
	}

	response.JSON(c, http.StatusOK, gin.H{"items": items})
}

func (h *apiHandler) deleteBucket(c *gin.Context) {
	if err := h.bucketService.Delete(c.Request.Context(), c.Param("bucket")); err != nil {
		response.Error(c, err)
		return
	}

	response.NoContent(c, http.StatusNoContent)
}

func (h *apiHandler) uploadObject(c *gin.Context) {
	allowOverwrite, err := parseOptionalBool(c.GetHeader("X-Allow-Overwrite"))
	if err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "X-Allow-Overwrite must be true or false"))
		return
	}

	object, err := h.objectService.Upload(c.Request.Context(), service.UploadObjectInput{
		BucketName:       c.Param("bucket"),
		ObjectKey:        normalizeObjectKey(c.Param("key")),
		Visibility:       c.GetHeader("X-Object-Visibility"),
		AllowOverwrite:   allowOverwrite,
		OriginalFilename: c.GetHeader("X-Original-Filename"),
		ContentType:      c.GetHeader("Content-Type"),
		Body:             c.Request.Body,
	})
	if err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			response.Error(c, apperrors.New(http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds configured upload size"))
			return
		}

		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusCreated, objectToResponse(*object))
}

func (h *apiHandler) listObjects(c *gin.Context) {
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
	result, err := h.objectService.List(c.Request.Context(), service.ListObjectsInput{
		BucketName: c.Param("bucket"),
		Prefix:     c.Query("prefix"),
		Limit:      limit,
		Cursor:     c.Query("cursor"),
	})
	if err != nil {
		response.Error(c, err)
		return
	}

	items := make([]objectResponse, 0, len(result.Items))
	for _, object := range result.Items {
		items = append(items, objectToResponse(object))
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items":       items,
		"next_cursor": result.NextCursor,
	})
}

func (h *apiHandler) updateObjectVisibility(c *gin.Context) {
	var req updateObjectVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}
	if req.Visibility == nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "visibility is required"))
		return
	}

	object, err := h.objectService.UpdateVisibility(
		c.Request.Context(),
		c.Param("bucket"),
		normalizeObjectKey(c.Param("key")),
		*req.Visibility,
	)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, objectToResponse(*object))
}

func (h *apiHandler) deleteObject(c *gin.Context) {
	if err := h.objectService.Delete(c.Request.Context(), c.Param("bucket"), normalizeObjectKey(c.Param("key"))); err != nil {
		response.Error(c, err)
		return
	}

	response.NoContent(c, http.StatusNoContent)
}

func (h *apiHandler) headObject(c *gin.Context) {
	h.serveObject(c, true)
}

func (h *apiHandler) downloadObject(c *gin.Context) {
	h.serveObject(c, false)
}

func (h *apiHandler) serveObject(c *gin.Context, headOnly bool) {
	bucketName := c.Param("bucket")
	objectKey := normalizeObjectKey(c.Param("key"))
	forceDownload, err := parseOptionalBoolQuery(c.Query("download"))
	if err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "download query is invalid"))
		return
	}

	object, reader, err := h.objectService.Open(c.Request.Context(), bucketName, objectKey)
	if err != nil {
		response.Error(c, err)
		return
	}
	defer func() {
		if reader != nil {
			_ = reader.Close()
		}
	}()

	if object.Visibility == model.VisibilityPrivate {
		if headOnly {
			if !h.authValidator.HasValidBearer(c) {
				response.Error(c, apperrors.New(http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token"))
				return
			}
		} else if !h.authValidator.HasValidBearer(c) {
			expiresAt, _ := strconv.ParseInt(c.Query("expires"), 10, 64)
			if err := h.signService.VerifyDownload(bucketName, objectKey, expiresAt, c.Query("signature")); err != nil {
				response.Error(c, err)
				return
			}
		}
	}

	setObjectHeaders(c, object, forceDownload)
	if headOnly {
		c.Status(http.StatusOK)
		return
	}

	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, reader); err != nil {
		h.logger.Error("stream object", zap.Error(err))
	}
}

func (h *apiHandler) signDownload(c *gin.Context) {
	var req signDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}
	expiresInSeconds := int64(0)
	if req.ExpiresInSeconds != nil {
		if *req.ExpiresInSeconds <= 0 {
			response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_expiry", "expires_in_seconds must be greater than zero"))
			return
		}
		expiresInSeconds = *req.ExpiresInSeconds
	}

	path, expiresAt, err := h.signService.GenerateDownloadPath(req.Bucket, req.ObjectKey, expiresInSeconds)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, gin.H{
		"path":       path,
		"expires_at": expiresAt,
	})
}

func bucketToResponse(bucket model.Bucket) bucketResponse {
	return bucketResponse{
		ID:        bucket.ID,
		Name:      bucket.Name,
		CreatedAt: bucket.CreatedAt,
		UpdatedAt: bucket.UpdatedAt,
	}
}

func objectToResponse(object model.Object) objectResponse {
	return objectResponse{
		ID:               object.ID,
		BucketName:       object.BucketName,
		ObjectKey:        object.ObjectKey,
		OriginalFilename: object.OriginalFilename,
		Size:             object.Size,
		ContentType:      object.ContentType,
		ETag:             object.ETag,
		Visibility:       string(object.Visibility),
		CreatedAt:        object.CreatedAt,
		UpdatedAt:        object.UpdatedAt,
	}
}
