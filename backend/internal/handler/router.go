package handler

import (
	"context"
	"database/sql"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"light-oss/backend/internal/config"
	"light-oss/backend/internal/middleware"
	"light-oss/backend/internal/model"
	"light-oss/backend/internal/service"
)

type Dependencies struct {
	Config              config.Config
	Logger              *zap.Logger
	DB                  *sql.DB
	AuthValidator       *middleware.TokenValidator
	RateLimitStore      middleware.RateLimitStore
	BucketService       *service.BucketService
	ObjectService       *service.ObjectService
	RecycleBinService   *service.RecycleBinService
	SiteService         *service.SiteService
	SitePublishService  *service.SitePublishService
	SignService         *service.SignService
	SystemStatsService  *service.SystemStatsService
	StorageQuotaService *service.StorageQuotaService
	RuntimeMetrics      *service.RuntimeMetrics
	ReadinessCheck      func(context.Context) error
}

type apiHandler struct {
	cfg                 config.Config
	logger              *zap.Logger
	db                  *sql.DB
	authValidator       *middleware.TokenValidator
	bucketService       *service.BucketService
	objectService       *service.ObjectService
	recycleBinService   *service.RecycleBinService
	siteService         *service.SiteService
	sitePublishService  *service.SitePublishService
	signService         *service.SignService
	systemStatsService  *service.SystemStatsService
	storageQuotaService *service.StorageQuotaService
	runtimeMetrics      *service.RuntimeMetrics
	readinessCheck      func(context.Context) error
	ipRateLimiter       *middleware.RateLimiter
	publicRateLimiter   *middleware.RateLimiter
	managementLimiter   *middleware.RateLimiter
	uploadRateLimiter   *middleware.RateLimiter
	signRateLimiter     *middleware.RateLimiter
	healthRateLimiter   *middleware.RateLimiter
}

func NewRouter(deps Dependencies) *gin.Engine {
	router := gin.New()
	router.MaxMultipartMemory = deps.Config.MaxMultipartMemoryBytes
	if err := router.SetTrustedProxies(deps.Config.TrustedProxies); err != nil {
		panic(err)
	}

	handler := &apiHandler{
		cfg:                 deps.Config,
		logger:              deps.Logger,
		db:                  deps.DB,
		authValidator:       deps.AuthValidator,
		bucketService:       deps.BucketService,
		objectService:       deps.ObjectService,
		recycleBinService:   deps.RecycleBinService,
		siteService:         deps.SiteService,
		sitePublishService:  deps.SitePublishService,
		signService:         deps.SignService,
		systemStatsService:  deps.SystemStatsService,
		storageQuotaService: deps.StorageQuotaService,
		runtimeMetrics:      deps.RuntimeMetrics,
		readinessCheck:      deps.ReadinessCheck,
	}

	cacheTTL := time.Duration(deps.Config.RateLimitCacheTTLSeconds) * time.Second
	newRateLimiter := func(namespace string, rps float64, burst int) *middleware.RateLimiter {
		if deps.Config.RateLimitBackend == config.RateLimitBackendMySQL {
			if deps.RateLimitStore == nil {
				panic("mysql rate limit backend requires a store")
			}
			return middleware.NewRateLimiterWithStore(namespace, rps, burst, cacheTTL, deps.RateLimitStore)
		}
		return middleware.NewRateLimiter(rps, burst, cacheTTL, deps.Config.RateLimitCacheMaxEntries)
	}
	ipRateLimiter := newRateLimiter("global-ip", deps.Config.RateLimitIPRPS, deps.Config.RateLimitIPBurst)
	publicRateLimiter := newRateLimiter("public", deps.Config.RateLimitPublicRPS, deps.Config.RateLimitPublicBurst)
	managementRateLimiter := newRateLimiter("management", deps.Config.RateLimitRPS, deps.Config.RateLimitBurst)
	uploadRateLimiter := newRateLimiter("upload", deps.Config.RateLimitUploadRPS, deps.Config.RateLimitUploadBurst)
	signRateLimiter := newRateLimiter("sign", deps.Config.RateLimitSignRPS, deps.Config.RateLimitSignBurst)
	healthRateLimiter := newRateLimiter("health", deps.Config.RateLimitHealthRPS, deps.Config.RateLimitHealthBurst)
	handler.ipRateLimiter = ipRateLimiter
	handler.publicRateLimiter = publicRateLimiter
	handler.managementLimiter = managementRateLimiter
	handler.uploadRateLimiter = uploadRateLimiter
	handler.signRateLimiter = signRateLimiter
	handler.healthRateLimiter = healthRateLimiter

	router.Use(middleware.RequestID())
	router.Use(middleware.ErrorLogger(deps.Logger))
	router.Use(middleware.RequestLogger(deps.Logger))
	router.Use(middleware.Recovery())
	router.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders:     []string{"Authorization", "Content-Type", "X-Object-Visibility", "X-Original-Filename", "X-Allow-Overwrite", "X-Request-ID"},
		ExposeHeaders:    []string{"Content-Disposition", "Content-Length", "Content-Type", "ETag", "X-Request-ID", "X-Object-Visibility", "X-Original-Filename"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))
	// Liveness only reports whether this process can serve HTTP. Register it
	// before the potentially database-backed limiter so dependency failures do
	// not cause the orchestrator to restart an otherwise healthy process.
	router.GET("/livez", handler.livez)
	healthCheckLimit := healthRateLimiter.LimitByClientIP()
	router.GET("/readyz", healthCheckLimit, handler.readyz)
	router.GET("/healthz", healthCheckLimit, handler.healthz)
	router.Use(ipRateLimiter.LimitByClientIP())

	registerRoutes(router, handler, deps, routeLimiters{
		public:     publicRateLimiter,
		management: managementRateLimiter,
		upload:     uploadRateLimiter,
		sign:       signRateLimiter,
		health:     healthRateLimiter,
	})

	return router
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
