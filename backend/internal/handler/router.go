package handler

import (
	"context"
	"database/sql"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"light-oss/backend/internal/config"
	"light-oss/backend/internal/middleware"
	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
	"light-oss/backend/internal/service"
)

type Dependencies struct {
	Config              config.Config
	Logger              *zap.Logger
	DB                  *sql.DB
	GormDB              *gorm.DB
	AuthValidator       *middleware.TokenValidator
	BucketService       *service.BucketService
	ObjectService       *service.ObjectService
	RecycleBinService   *service.RecycleBinService
	SiteService         *service.SiteService
	SitePublishService  *service.SitePublishService
	SignService         *service.SignService
	SystemStatsService  *service.SystemStatsService
	StorageQuotaService *service.StorageQuotaService
}

type apiHandler struct {
	cfg                 config.Config
	logger              *zap.Logger
	db                  *sql.DB
	gormDB              *gorm.DB
	authValidator       *middleware.TokenValidator
	bucketService       *service.BucketService
	objectService       *service.ObjectService
	recycleBinService   *service.RecycleBinService
	siteService         *service.SiteService
	sitePublishService  *service.SitePublishService
	signService         *service.SignService
	systemStatsService  *service.SystemStatsService
	storageQuotaService *service.StorageQuotaService
}

type createBucketRequest struct {
	Name string `json:"name"`
}

type createFolderRequest struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
}

type updateObjectVisibilityRequest struct {
	Visibility string `json:"visibility"`
}

type updateStorageQuotaRequest struct {
	MaxBytes *int64 `json:"max_bytes"`
}

type signDownloadRequest struct {
	Bucket           string `json:"bucket"`
	ObjectKey        string `json:"object_key"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
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

type siteRequest struct {
	Bucket        string   `json:"bucket"`
	RootPrefix    string   `json:"root_prefix"`
	Enabled       *bool    `json:"enabled"`
	IndexDocument string   `json:"index_document"`
	ErrorDocument string   `json:"error_document"`
	SPAFallback   *bool    `json:"spa_fallback"`
	Domains       []string `json:"domains"`
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

type siteResponse struct {
	ID            uint64    `json:"id"`
	Bucket        string    `json:"bucket"`
	RootPrefix    string    `json:"root_prefix"`
	Enabled       bool      `json:"enabled"`
	IndexDocument string    `json:"index_document"`
	ErrorDocument string    `json:"error_document"`
	SPAFallback   bool      `json:"spa_fallback"`
	Domains       []string  `json:"domains"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type sitePublishResponse struct {
	UploadedCount int          `json:"uploaded_count"`
	Site          siteResponse `json:"site"`
}

type systemStatsResponse struct {
	OS      string                `json:"os"`
	CPU     systemCPUResponse     `json:"cpu"`
	Memory  systemMemoryResponse  `json:"memory"`
	Disks   []systemDiskResponse  `json:"disks"`
	Storage systemStorageResponse `json:"storage"`
}

type systemCPUResponse struct {
	UsedPercent float64 `json:"used_percent"`
}

type systemMemoryResponse struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

type systemDiskResponse struct {
	Label               string  `json:"label"`
	MountPoint          string  `json:"mount_point"`
	Filesystem          string  `json:"filesystem"`
	TotalBytes          uint64  `json:"total_bytes"`
	UsedBytes           uint64  `json:"used_bytes"`
	FreeBytes           uint64  `json:"free_bytes"`
	UsedPercent         float64 `json:"used_percent"`
	ContainsStorageRoot bool    `json:"contains_storage_root"`
}

type systemStorageResponse struct {
	RootPath       string  `json:"root_path"`
	UsedBytes      uint64  `json:"used_bytes"`
	MaxBytes       uint64  `json:"max_bytes"`
	RemainingBytes uint64  `json:"remaining_bytes"`
	UsedPercent    float64 `json:"used_percent"`
	LimitStatus    string  `json:"limit_status"`
}

func NewRouter(deps Dependencies) *gin.Engine {
	router := gin.New()
	router.MaxMultipartMemory = deps.Config.MaxMultipartMemoryBytes

	handler := &apiHandler{
		cfg:                 deps.Config,
		logger:              deps.Logger,
		db:                  deps.DB,
		gormDB:              deps.GormDB,
		authValidator:       deps.AuthValidator,
		bucketService:       deps.BucketService,
		objectService:       deps.ObjectService,
		recycleBinService:   deps.RecycleBinService,
		siteService:         deps.SiteService,
		sitePublishService:  deps.SitePublishService,
		signService:         deps.SignService,
		systemStatsService:  deps.SystemStatsService,
		storageQuotaService: deps.StorageQuotaService,
	}

	rateLimiter := middleware.NewRateLimiter(deps.Config.RateLimitRPS, deps.Config.RateLimitBurst)

	router.Use(middleware.RequestID())
	router.Use(gin.Recovery())
	router.Use(middleware.RequestLogger(deps.Logger))
	router.Use(rateLimiter.Middleware())
	router.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders:     []string{"Authorization", "Content-Type", "X-Object-Visibility", "X-Original-Filename", "X-Allow-Overwrite", "X-Request-ID"},
		ExposeHeaders:    []string{"Content-Disposition", "Content-Length", "Content-Type", "ETag", "X-Request-ID", "X-Object-Visibility", "X-Original-Filename"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))

	router.GET("/healthz", handler.healthz)
	router.GET("/sites/:siteID", handler.downloadSite)
	router.HEAD("/sites/:siteID", handler.headSite)
	router.GET("/sites/:siteID/*path", handler.downloadSite)
	router.HEAD("/sites/:siteID/*path", handler.headSite)
	router.NoRoute(handler.noRoute)

	api := router.Group("/api/v1")
	api.GET("/buckets/:bucket/objects/*key", handler.downloadObject)
	api.HEAD("/buckets/:bucket/objects/*key", handler.headObject)

	protected := api.Group("")
	protected.Use(deps.AuthValidator.RequireBearer())
	protected.GET("/healthz", handler.healthz)
	protected.GET("/system/stats", handler.systemStats)
	protected.PUT("/system/storage/quota", handler.updateStorageQuota)
	protected.POST("/buckets", handler.createBucket)
	protected.GET("/buckets", handler.listBuckets)
	protected.DELETE("/buckets/:bucket", handler.deleteBucket)
	protected.GET("/buckets/:bucket/folders", handler.listFolders)
	protected.GET("/buckets/:bucket/folders/archive", handler.downloadFolderArchive)
	protected.POST("/buckets/:bucket/folders", handler.createFolder)
	protected.DELETE("/buckets/:bucket/folders", handler.deleteFolder)
	protected.GET("/buckets/:bucket/entries", handler.listExplorerEntries)
	protected.POST("/buckets/:bucket/entries/batch-delete", handler.deleteExplorerEntriesBatch)
	protected.POST("/buckets/:bucket/objects/batch", middleware.MaxBodySize(deps.Config.MaxUploadSizeBytes), handler.uploadObjectBatch)
	protected.PUT("/buckets/:bucket/objects/*key", middleware.MaxBodySize(deps.Config.MaxUploadSizeBytes), handler.uploadObject)
	protected.PATCH("/buckets/:bucket/objects/visibility/*key", handler.updateObjectVisibility)
	protected.GET("/buckets/:bucket/objects", handler.listObjects)
	protected.DELETE("/buckets/:bucket/objects/*key", handler.deleteObject)
	protected.GET("/recycle-bin/objects", handler.listRecycleBinObjects)
	protected.POST("/recycle-bin/objects/restore", handler.restoreRecycleBinObjects)
	protected.POST("/recycle-bin/objects/batch-delete", handler.deleteRecycleBinObjects)
	protected.POST("/sites", handler.createSite)
	protected.POST("/sites/publish/object", handler.publishObjectSite)
	protected.POST("/sites/publish/file", middleware.MaxBodySize(deps.Config.MaxUploadSizeBytes), handler.publishSiteFile)
	protected.POST("/sites/publish", middleware.MaxBodySize(deps.Config.MaxUploadSizeBytes), handler.publishSite)
	protected.GET("/sites", handler.listSites)
	protected.GET("/sites/:siteID", handler.getSite)
	protected.PUT("/sites/:siteID", handler.updateSite)
	protected.DELETE("/sites/:siteID", handler.deleteSite)
	protected.POST("/sign/download", handler.signDownload)

	return router
}

func (h *apiHandler) healthz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	dbStatus := "ok"
	statusCode := http.StatusOK
	if _, err := h.bucketService.List(ctx, ""); err != nil {
		dbStatus = "error"
		statusCode = http.StatusServiceUnavailable
		h.logger.Error("healthz bucket query failed", zap.Error(err))
	}

	response.JSON(c, statusCode, gin.H{
		"status": gin.H{
			"service": "ok",
			"db":      dbStatus,
		},
		"version": "mvp",
	})
}

func (h *apiHandler) systemStats(c *gin.Context) {
	stats, err := h.systemStatsService.Collect(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, systemStatsToResponse(*stats))
}

func (h *apiHandler) updateStorageQuota(c *gin.Context) {
	var req updateStorageQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}
	if req.MaxBytes == nil || *req.MaxBytes <= 0 {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "max_bytes must be greater than zero"))
		return
	}

	quota, err := h.storageQuotaService.UpdateMaxBytes(c.Request.Context(), uint64(*req.MaxBytes))
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, storageQuotaToResponse(*quota))
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

func (h *apiHandler) createSite(c *gin.Context) {
	var req siteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}

	site, err := h.siteService.Create(c.Request.Context(), siteInputFromRequest(req))
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusCreated, siteToResponse(*site))
}

func (h *apiHandler) listSites(c *gin.Context) {
	sites, err := h.siteService.List(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}

	items := make([]siteResponse, 0, len(sites))
	for _, site := range sites {
		items = append(items, siteToResponse(site))
	}

	response.JSON(c, http.StatusOK, gin.H{"items": items})
}

func (h *apiHandler) getSite(c *gin.Context) {
	siteID, err := service.ParseSiteID(c.Param("siteID"))
	if err != nil {
		response.Error(c, err)
		return
	}

	site, err := h.siteService.Get(c.Request.Context(), siteID)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, siteToResponse(*site))
}

func (h *apiHandler) updateSite(c *gin.Context) {
	siteID, err := service.ParseSiteID(c.Param("siteID"))
	if err != nil {
		response.Error(c, err)
		return
	}

	var req siteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}

	site, err := h.siteService.Update(c.Request.Context(), siteID, siteInputFromRequest(req))
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, siteToResponse(*site))
}

func (h *apiHandler) deleteSite(c *gin.Context) {
	siteID, err := service.ParseSiteID(c.Param("siteID"))
	if err != nil {
		response.Error(c, err)
		return
	}

	if err := h.siteService.Delete(c.Request.Context(), siteID); err != nil {
		response.Error(c, err)
		return
	}

	response.NoContent(c, http.StatusNoContent)
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

	if err := archive.WriteTo(c.Writer); err != nil {
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
	limit, _ := strconv.Atoi(c.Query("limit"))
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

	object, err := h.objectService.UpdateVisibility(
		c.Request.Context(),
		c.Param("bucket"),
		normalizeObjectKey(c.Param("key")),
		req.Visibility,
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
		Limit:  limit,
		Cursor: c.Query("cursor"),
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

func (h *apiHandler) headSite(c *gin.Context) {
	h.serveSiteByID(c, true)
}

func (h *apiHandler) downloadSite(c *gin.Context) {
	h.serveSiteByID(c, false)
}

func (h *apiHandler) serveSiteByID(c *gin.Context, headOnly bool) {
	siteID, err := service.ParseSiteID(c.Param("siteID"))
	if err != nil {
		status := apperrors.From(err).Status
		c.Status(status)
		return
	}

	site, err := h.siteService.Get(c.Request.Context(), siteID)
	if err != nil {
		h.writeWebsiteError(c, err)
		return
	}

	h.serveSiteContent(c, site, c.Param("path"), headOnly)
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

func (h *apiHandler) noRoute(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Status(http.StatusNotFound)
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/api/") || c.Request.URL.Path == "/healthz" {
		c.Status(http.StatusNotFound)
		return
	}

	site, err := h.siteService.FindByDomain(c.Request.Context(), c.Request.Host)
	if err != nil {
		h.writeWebsiteError(c, err)
		return
	}
	if site == nil {
		c.Status(http.StatusNotFound)
		return
	}

	h.serveSiteContent(c, site, c.Request.URL.Path, c.Request.Method == http.MethodHead)
}

func (h *apiHandler) serveSiteContent(c *gin.Context, site *model.Site, requestPath string, headOnly bool) {
	content, err := h.siteService.OpenContent(c.Request.Context(), site, requestPath)
	if err != nil {
		h.writeWebsiteError(c, err)
		return
	}
	defer func() {
		if content.Reader != nil {
			_ = content.Reader.Close()
		}
	}()

	setObjectHeaders(c, content.Object, false)
	c.Status(content.StatusCode)
	if headOnly {
		return
	}

	if _, err := io.Copy(c.Writer, content.Reader); err != nil {
		h.logger.Error("stream site content", zap.Error(err))
	}
}

func (h *apiHandler) writeWebsiteError(c *gin.Context, err error) {
	appErr := apperrors.From(err)
	if appErr.Status >= http.StatusInternalServerError {
		h.logger.Error("serve website", zap.Error(err))
	}
	c.Status(appErr.Status)
}

func (h *apiHandler) signDownload(c *gin.Context) {
	var req signDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}

	path, expiresAt, err := h.signService.GenerateDownloadPath(req.Bucket, req.ObjectKey, req.ExpiresInSeconds)
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

func siteToResponse(site model.Site) siteResponse {
	domains := make([]string, 0, len(site.Domains))
	for _, domain := range site.Domains {
		domains = append(domains, domain.Domain)
	}

	return siteResponse{
		ID:            site.ID,
		Bucket:        site.BucketName,
		RootPrefix:    site.RootPrefix,
		Enabled:       site.Enabled,
		IndexDocument: site.IndexDocument,
		ErrorDocument: site.ErrorDocument,
		SPAFallback:   site.SPAFallback,
		Domains:       domains,
		CreatedAt:     site.CreatedAt,
		UpdatedAt:     site.UpdatedAt,
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

func systemStatsToResponse(stats service.SystemStats) systemStatsResponse {
	disks := make([]systemDiskResponse, 0, len(stats.Disks))
	for _, item := range stats.Disks {
		disks = append(disks, systemDiskResponse{
			Label:               item.Label,
			MountPoint:          item.MountPoint,
			Filesystem:          item.Filesystem,
			TotalBytes:          item.TotalBytes,
			UsedBytes:           item.UsedBytes,
			FreeBytes:           item.FreeBytes,
			UsedPercent:         item.UsedPercent,
			ContainsStorageRoot: item.ContainsStorageRoot,
		})
	}

	return systemStatsResponse{
		OS: stats.OS,
		CPU: systemCPUResponse{
			UsedPercent: stats.CPU.UsedPercent,
		},
		Memory: systemMemoryResponse{
			TotalBytes:     stats.Memory.TotalBytes,
			UsedBytes:      stats.Memory.UsedBytes,
			AvailableBytes: stats.Memory.AvailableBytes,
			UsedPercent:    stats.Memory.UsedPercent,
		},
		Disks: disks,
		Storage: systemStorageResponse{
			RootPath:       stats.Storage.RootPath,
			UsedBytes:      stats.Storage.UsedBytes,
			MaxBytes:       stats.Storage.MaxBytes,
			RemainingBytes: stats.Storage.RemainingBytes,
			UsedPercent:    stats.Storage.UsedPercent,
			LimitStatus:    string(stats.Storage.LimitStatus),
		},
	}
}

func storageQuotaToResponse(stats service.StorageQuotaSnapshot) systemStorageResponse {
	return systemStorageResponse{
		RootPath:       stats.RootPath,
		UsedBytes:      stats.UsedBytes,
		MaxBytes:       stats.MaxBytes,
		RemainingBytes: stats.RemainingBytes,
		UsedPercent:    stats.UsedPercent,
		LimitStatus:    string(stats.LimitStatus),
	}
}

func normalizeObjectKey(raw string) string {
	return strings.TrimPrefix(raw, "/")
}

func parseOptionalBool(raw string) (bool, error) {
	if strings.TrimSpace(raw) == "" {
		return false, nil
	}

	return strconv.ParseBool(raw)
}

func parseOptionalBoolQuery(raw string) (bool, error) {
	return parseOptionalBool(raw)
}

func siteInputFromRequest(req siteRequest) service.SiteInput {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	spaFallback := false
	if req.SPAFallback != nil {
		spaFallback = *req.SPAFallback
	}

	return service.SiteInput{
		BucketName:    req.Bucket,
		RootPrefix:    req.RootPrefix,
		Enabled:       enabled,
		IndexDocument: req.IndexDocument,
		ErrorDocument: req.ErrorDocument,
		SPAFallback:   spaFallback,
		Domains:       req.Domains,
	}
}

func setObjectHeaders(c *gin.Context, object *model.Object, forceDownload bool) {
	c.Header("Content-Type", service.NormalizeContentType(object.ContentType))
	c.Header("Content-Length", strconv.FormatInt(object.Size, 10))
	c.Header("ETag", object.ETag)
	c.Header("X-Object-Visibility", string(object.Visibility))
	c.Header("X-Original-Filename", url.PathEscape(object.OriginalFilename))
	dispositionType := "inline"
	if forceDownload {
		dispositionType = "attachment"
	}
	if contentDisposition := mime.FormatMediaType(dispositionType, map[string]string{
		"filename": object.OriginalFilename,
	}); contentDisposition != "" {
		c.Header("Content-Disposition", contentDisposition)
	}
}

func setFolderArchiveHeaders(c *gin.Context, filename string) {
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": filename,
	}))
}
