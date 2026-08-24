package handler_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"light-oss/backend/internal/config"
	"light-oss/backend/internal/handler"
	"light-oss/backend/internal/middleware"
	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/service"
	"light-oss/backend/internal/signing"
	"light-oss/backend/internal/storage"
)

type apiEnvelope[T any] struct {
	Data  T             `json:"data"`
	Error *apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type bucketResponse struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

type bucketListResponse struct {
	Items []bucketResponse `json:"items"`
}

type objectResponse struct {
	ObjectKey        string `json:"object_key"`
	OriginalFilename string `json:"original_filename"`
	Visibility       string `json:"visibility"`
	Size             int64  `json:"size"`
}

type objectListResponse struct {
	Items      []objectResponse `json:"items"`
	NextCursor string           `json:"next_cursor"`
}

type folderNodeResponse struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	ParentPath string `json:"parent_path"`
}

type folderListResponse struct {
	Items []folderNodeResponse `json:"items"`
}

type explorerEntryResponse struct {
	Type      string     `json:"type"`
	Path      string     `json:"path"`
	Name      string     `json:"name"`
	IsEmpty   *bool      `json:"is_empty"`
	ObjectKey *string    `json:"object_key"`
	CreatedAt *time.Time `json:"created_at"`
}

type explorerListResponse struct {
	Items      []explorerEntryResponse `json:"items"`
	NextCursor string                  `json:"next_cursor"`
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
	ID         uint64 `json:"id"`
	Type       string `json:"type"`
	BucketName string `json:"bucket_name"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	ObjectKey  string `json:"object_key"`
	Size       int64  `json:"size"`
}

type recycleBinListResponse struct {
	Items      []recycleBinObjectResponse `json:"items"`
	NextCursor string                     `json:"next_cursor"`
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

type signResponse struct {
	Path string `json:"path"`
}

type siteResponse struct {
	ID            uint64   `json:"id"`
	Bucket        string   `json:"bucket"`
	RootPrefix    string   `json:"root_prefix"`
	Enabled       bool     `json:"enabled"`
	IndexDocument string   `json:"index_document"`
	ErrorDocument string   `json:"error_document"`
	SPAFallback   bool     `json:"spa_fallback"`
	Domains       []string `json:"domains"`
}

type siteListResponse struct {
	Items []siteResponse `json:"items"`
}

type uploadBatchResponse struct {
	UploadedCount int              `json:"uploaded_count"`
	Items         []objectResponse `json:"items"`
}

type publishSiteResponse struct {
	UploadedCount int          `json:"uploaded_count"`
	Site          siteResponse `json:"site"`
}

func newTestRouter(t *testing.T, maxUploadSize int64) *gin.Engine {
	router, _ := newTestRouterWithStorageRoot(t, maxUploadSize)
	return router
}

func newTestRouterWithConfig(t *testing.T, maxUploadSize int64, configure func(*config.Config)) *gin.Engine {
	router, _, _ := newTestRouterWithStorageRootAndDBConfig(t, maxUploadSize, configure)
	return router
}

func newTestRouterWithStorageRoot(t *testing.T, maxUploadSize int64) (*gin.Engine, string) {
	router, root, _ := newTestRouterWithStorageRootAndDB(t, maxUploadSize)
	return router, root
}

func newTestRouterWithStorageRootAndDB(t *testing.T, maxUploadSize int64) (*gin.Engine, string, *gorm.DB) {
	return newTestRouterWithStorageRootAndDBConfig(t, maxUploadSize, nil)
}

func newTestRouterWithStorageRootAndDBConfig(
	t *testing.T,
	maxUploadSize int64,
	configure func(*config.Config),
) (*gin.Engine, string, *gorm.DB) {
	return newTestRouterWithStorageRootAndDBConfigAndRateLimitStore(t, maxUploadSize, configure, nil)
}

func newTestRouterWithStorageRootAndDBConfigAndRateLimitStore(
	t *testing.T,
	maxUploadSize int64,
	configure func(*config.Config),
	rateLimitStore middleware.RateLimitStore,
) (*gin.Engine, string, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatalf("enable sqlite foreign keys: %v", err)
	}

	if err := db.AutoMigrate(
		&model.Bucket{},
		&model.SystemStorageQuota{},
		&model.Object{},
		&model.RecycleBinObject{},
		&model.Site{},
		&model.SiteDomain{},
		&model.StorageBlob{},
		&model.StorageCleanupJob{},
	); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}

	root := t.TempDir()
	cfg := config.Config{
		AppEnv:                     "development",
		AppAddr:                    ":0",
		StorageRoot:                filepath.ToSlash(root),
		MaxUploadSizeBytes:         maxUploadSize,
		MaxMultipartMemoryBytes:    8 * 1024 * 1024,
		RateLimitIPRPS:             1000,
		RateLimitIPBurst:           1000,
		RateLimitManagementRPS:     1000,
		RateLimitManagementBurst:   1000,
		RateLimitPublicRPS:         1000,
		RateLimitPublicBurst:       1000,
		RateLimitUploadRPS:         1000,
		RateLimitUploadBurst:       1000,
		RateLimitSignRPS:           1000,
		RateLimitSignBurst:         1000,
		RateLimitHealthRPS:         1000,
		RateLimitHealthBurst:       1000,
		RateLimitCacheTTLSeconds:   3600,
		RateLimitCacheMaxEntries:   10000,
		BearerTokens:               []string{"dev-token"},
		SigningSecret:              "test-secret",
		DefaultSignedURLTTLSeconds: 300,
		MaxSignedURLTTLSeconds:     86400,
	}
	if configure != nil {
		configure(&cfg)
	}

	bucketRepo := repository.NewBucketRepository(db)
	objectRepo := repository.NewObjectRepository(db)
	recycleRepo := repository.NewRecycleBinRepository(db)
	siteRepo := repository.NewSiteRepository(db)
	localStorage := storage.NewLocalStorage(root)
	storageQuotaRepo := repository.NewStorageQuotaRepository(db)
	if _, err := storageQuotaRepo.EnsureDefault(context.Background(), 10*1024*1024*1024); err != nil {
		t.Fatalf("initialize storage quota: %v", err)
	}
	storageBlobRepo := repository.NewStorageBlobRepository(db)
	blobLifecycle := service.NewBlobLifecycleService(zap.NewNop(), db, localStorage, storageBlobRepo, 1024*1024)
	cleanupWorker := service.NewStorageCleanupWorker(zap.NewNop(), localStorage, storageBlobRepo, time.Hour)
	blobLifecycle.SetCleanupRunOnce(func(ctx context.Context) error {
		return cleanupWorker.RunOnce(ctx, time.Now().UTC())
	})
	storageQuotaService := service.NewStorageQuotaService(root, storageQuotaRepo)
	objectService := service.NewObjectService(db, bucketRepo, objectRepo, recycleRepo, localStorage, blobLifecycle)
	recycleBinService := service.NewRecycleBinService(db, bucketRepo, objectRepo, recycleRepo, blobLifecycle)
	siteService := service.NewSiteService(bucketRepo, siteRepo, objectService)
	return handler.NewRouter(handler.Dependencies{
		Config:              cfg,
		Logger:              zap.NewNop(),
		DB:                  sqlDB,
		AuthValidator:       middleware.NewTokenValidator(cfg.BearerTokens),
		RateLimitStore:      rateLimitStore,
		BucketService:       service.NewBucketService(bucketRepo, objectRepo, recycleRepo, siteRepo, blobLifecycle),
		ObjectService:       objectService,
		RecycleBinService:   recycleBinService,
		SiteService:         siteService,
		SitePublishService:  service.NewSitePublishService(db, objectRepo, siteRepo, blobLifecycle, siteService),
		SignService:         service.NewSignService(signing.NewSigner(cfg.SigningSecret), cfg.DefaultSignedURLTTLSeconds, cfg.MaxSignedURLTTLSeconds),
		SystemStatsService:  service.NewSystemStatsService(zap.NewNop(), storageQuotaService),
		StorageQuotaService: storageQuotaService,
	}), root, db
}

type failingRateLimitStore struct{}

func (failingRateLimitStore) Allow(context.Context, string, float64, int, time.Duration) (bool, error) {
	return false, fmt.Errorf("rate limit store unavailable")
}

type healthOnlyRateLimitStore struct{}

func (healthOnlyRateLimitStore) Allow(_ context.Context, key string, _ float64, _ int, _ time.Duration) (bool, error) {
	if strings.HasPrefix(key, "health:") {
		return true, nil
	}
	return false, fmt.Errorf("unexpected non-health rate limit namespace %q", key)
}

func createBucket(t *testing.T, router *gin.Engine, name string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/buckets", bytes.NewBufferString(`{"name":"`+name+`"}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bucket expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func uploadObject(t *testing.T, router *gin.Engine, path string, body string, visibility string) {
	t.Helper()
	uploadObjectWithContentType(t, router, path, body, visibility, "text/plain")
}

func uploadObjectWithContentType(t *testing.T, router *gin.Engine, path string, body string, visibility string, contentType string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-Object-Visibility", visibility)
	req.Header.Set("X-Original-Filename", "file.txt")
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func createSite(t *testing.T, router *gin.Engine, payload string) siteResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewBufferString(payload))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create site expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[siteResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	return body.Data
}

func createFolder(t *testing.T, router *gin.Engine, bucket string, prefix string, name string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/buckets/"+bucket+"/folders", bytes.NewBufferString(`{"prefix":"`+prefix+`","name":"`+name+`"}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create folder expected 201, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func decodeJSON(t *testing.T, body []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode json: %v, body=%s", err, string(body))
	}
}

func assertAPIErrorCode(t *testing.T, body []byte, code string) {
	t.Helper()

	var envelope apiEnvelope[map[string]any]
	decodeJSON(t, body, &envelope)
	if envelope.Error == nil || envelope.Error.Code != code {
		t.Fatalf("expected error code %q, got %+v", code, envelope.Error)
	}
}

func unzipEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}

	entries := make(map[string]string, len(reader.File))
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			entries[file.Name] = ""
			continue
		}

		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", file.Name, err)
		}

		content, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", file.Name, err)
		}
		if closeErr != nil {
			t.Fatalf("close zip entry %s: %v", file.Name, closeErr)
		}

		entries[file.Name] = string(content)
	}

	return entries
}

type multipartUploadFile struct {
	Filename    string
	Content     string
	ContentType string
}

func newMultipartBatchUploadRequest(
	t *testing.T,
	targetURL string,
	fields map[string]string,
	files map[string]multipartUploadFile,
) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}

	for fieldName, file := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, file.Filename))
		if file.ContentType != "" {
			header.Set("Content-Type", file.ContentType)
		}

		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatalf("create file part %s: %v", fieldName, err)
		}
		if _, err := part.Write([]byte(file.Content)); err != nil {
			t.Fatalf("write file part %s: %v", fieldName, err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body.Bytes()))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	return string(raw)
}

func countFilesUnderRoot(t *testing.T, root string) int {
	t.Helper()

	count := 0
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		count++
		return nil
	}); err != nil {
		t.Fatalf("walk storage root: %v", err)
	}

	return count
}
