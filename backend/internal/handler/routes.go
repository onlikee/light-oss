package handler

import (
	"github.com/gin-gonic/gin"

	"light-oss/backend/internal/middleware"
)

type routeLimiters struct {
	public     *middleware.RateLimiter
	management *middleware.RateLimiter
	upload     *middleware.RateLimiter
	sign       *middleware.RateLimiter
	health     *middleware.RateLimiter
}

func registerRoutes(router *gin.Engine, handler *apiHandler, deps Dependencies, limiters routeLimiters) {
	publicDownloadLimit := limiters.public.LimitByClientIP()
	registerPublicRoutes(router, handler, publicDownloadLimit)

	api := router.Group("/api/v1")
	registerPublicObjectRoutes(api, handler, publicDownloadLimit)

	protected := api.Group("")
	protected.Use(deps.AuthValidator.RequireBearer())

	health := protected.Group("")
	health.Use(limiters.health.LimitByAuthenticatedBearer())
	registerHealthRoutes(health, handler)

	management := protected.Group("")
	management.Use(limiters.management.LimitByAuthenticatedBearer())
	registerSystemRoutes(management, handler)
	registerBucketRoutes(management, handler)
	registerExplorerRoutes(management, handler)
	registerObjectRoutes(management, handler)
	registerRecycleRoutes(management, handler)
	registerSiteRoutes(management, handler)

	upload := protected.Group("")
	upload.Use(limiters.upload.LimitByAuthenticatedBearer())
	registerObjectUploadRoutes(upload, handler, deps)
	registerSiteUploadRoutes(upload, handler, deps)

	sign := protected.Group("")
	sign.Use(limiters.sign.LimitByAuthenticatedBearer())
	registerSignRoutes(sign, handler)
}

func registerPublicRoutes(
	router *gin.Engine,
	handler *apiHandler,
	publicDownloadLimit gin.HandlerFunc,
) {
	router.GET("/sites/:siteID", publicDownloadLimit, handler.downloadSite)
	router.HEAD("/sites/:siteID", publicDownloadLimit, handler.headSite)
	router.GET("/sites/:siteID/*path", publicDownloadLimit, handler.downloadSite)
	router.HEAD("/sites/:siteID/*path", publicDownloadLimit, handler.headSite)
	router.NoRoute(publicDownloadLimit, handler.noRoute)
}

func registerPublicObjectRoutes(api *gin.RouterGroup, handler *apiHandler, publicDownloadLimit gin.HandlerFunc) {
	api.GET("/buckets/:bucket/objects/*key", publicDownloadLimit, handler.downloadObject)
	api.HEAD("/buckets/:bucket/objects/*key", publicDownloadLimit, handler.headObject)
}

func registerHealthRoutes(health *gin.RouterGroup, handler *apiHandler) {
	health.GET("/healthz", handler.healthz)
}

func registerSystemRoutes(management *gin.RouterGroup, handler *apiHandler) {
	management.GET("/system/metrics", handler.systemMetrics)
	management.GET("/system/stats", handler.systemStats)
	management.PUT("/system/storage/quota", handler.updateStorageQuota)
}

func registerBucketRoutes(management *gin.RouterGroup, handler *apiHandler) {
	management.POST("/buckets", handler.createBucket)
	management.GET("/buckets", handler.listBuckets)
	management.DELETE("/buckets/:bucket", handler.deleteBucket)
}

func registerExplorerRoutes(management *gin.RouterGroup, handler *apiHandler) {
	management.GET("/buckets/:bucket/folders", handler.listFolders)
	management.GET("/buckets/:bucket/folders/archive", handler.downloadFolderArchive)
	management.POST("/buckets/:bucket/folders", handler.createFolder)
	management.DELETE("/buckets/:bucket/folders", handler.deleteFolder)
	management.GET("/buckets/:bucket/entries", handler.listExplorerEntries)
	management.POST("/buckets/:bucket/entries/batch-delete", handler.deleteExplorerEntriesBatch)
}

func registerObjectRoutes(management *gin.RouterGroup, handler *apiHandler) {
	management.PATCH("/buckets/:bucket/objects/visibility/*key", handler.updateObjectVisibility)
	management.GET("/buckets/:bucket/objects", handler.listObjects)
	management.DELETE("/buckets/:bucket/objects/*key", handler.deleteObject)
}

func registerRecycleRoutes(management *gin.RouterGroup, handler *apiHandler) {
	management.GET("/recycle-bin/objects", handler.listRecycleBinObjects)
	management.POST("/recycle-bin/objects/restore", handler.restoreRecycleBinObjects)
	management.POST("/recycle-bin/objects/batch-delete", handler.deleteRecycleBinObjects)
}

func registerSiteRoutes(management *gin.RouterGroup, handler *apiHandler) {
	management.POST("/sites", handler.createSite)
	management.POST("/sites/publish/object", handler.publishObjectSite)
	management.GET("/sites", handler.listSites)
	management.GET("/sites/:siteID", handler.getSite)
	management.PUT("/sites/:siteID", handler.updateSite)
	management.DELETE("/sites/:siteID", handler.deleteSite)
}

func registerObjectUploadRoutes(upload *gin.RouterGroup, handler *apiHandler, deps Dependencies) {
	upload.POST("/buckets/:bucket/objects/batch", middleware.MaxBodySize(deps.Config.MaxUploadSizeBytes), handler.uploadObjectBatch)
	upload.PUT("/buckets/:bucket/objects/*key", middleware.MaxBodySize(deps.Config.MaxUploadSizeBytes), handler.uploadObject)
}

func registerSiteUploadRoutes(upload *gin.RouterGroup, handler *apiHandler, deps Dependencies) {
	upload.POST("/sites/publish/file", middleware.MaxBodySize(deps.Config.MaxUploadSizeBytes), handler.publishSiteFile)
	upload.POST("/sites/publish", middleware.MaxBodySize(deps.Config.MaxUploadSizeBytes), handler.publishSite)
}

func registerSignRoutes(sign *gin.RouterGroup, handler *apiHandler) {
	sign.POST("/sign/download", handler.signDownload)
}
