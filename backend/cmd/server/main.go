package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"go.uber.org/zap"
	gormmysql "gorm.io/driver/mysql"
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

func main() {
	reconcileOnly := flag.Bool("reconcile-storage-only", false, "reconcile managed storage and exit without starting the HTTP server")
	orphanCleanupID := flag.String("enqueue-orphan-cleanup-id", "", "enqueue one confirmed orphan blob ID for cleanup and exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logger, err := buildLogger(cfg)
	if err != nil {
		log.Fatalf("build logger: %v", err)
	}
	defer func() {
		_ = logger.Sync()
	}()
	if *reconcileOnly && strings.TrimSpace(*orphanCleanupID) != "" {
		logger.Fatal("reconciliation-only and orphan-cleanup modes cannot be used together")
	}

	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	storageDependencies, err := newRuntimeStorageDependencies(cfg)
	if err != nil {
		logger.Fatal("configure blob store", zap.Error(err))
	}

	sqlDB, err := openSQLDB(cfg)
	if err != nil {
		logger.Fatal("open database", zap.Error(err))
	}
	defer func() {
		_ = sqlDB.Close()
	}()

	if err := waitForDatabase(sqlDB, 30, 2*time.Second); err != nil {
		logger.Fatal("wait for database", zap.Error(err))
	}

	if err := runMigrations(sqlDB); err != nil {
		logger.Fatal("run migrations", zap.Error(err))
	}

	gormDB, err := openGormDB(sqlDB)
	if err != nil {
		logger.Fatal("open gorm database", zap.Error(err))
	}

	tokenValidator := middleware.NewTokenValidator(cfg.BearerTokens)
	var rateLimitStore middleware.RateLimitStore
	if cfg.RateLimitBackend == config.RateLimitBackendMySQL {
		rateLimitStore = middleware.NewMySQLRateLimitStore(sqlDB, cfg.RateLimitCacheMaxEntries)
	}
	bucketRepo := repository.NewBucketRepository(gormDB)
	objectRepo := repository.NewObjectRepository(gormDB)
	recycleRepo := repository.NewRecycleBinRepository(gormDB)
	siteRepo := repository.NewSiteRepository(gormDB)
	storageQuotaRepo := repository.NewStorageQuotaRepository(gormDB)
	storageID, err := storageDependencies.identity.Identity(context.Background())
	if err != nil {
		logger.Fatal("read storage identity", zap.Error(err))
	}
	storageQuota, err := storageQuotaRepo.Get(context.Background())
	if err != nil {
		logger.Fatal("load storage identity binding", zap.Error(err))
	}
	if storageQuota.StorageID != nil {
		if err := storageQuotaRepo.BindStorageIdentity(context.Background(), storageID); err != nil {
			logger.Fatal("verify storage identity", zap.Error(err))
		}
	}
	storageQuotaService := service.NewStorageQuotaService(cfg.StorageRoot, storageQuotaRepo)
	if _, err := storageQuotaService.Snapshot(context.Background()); err != nil {
		logger.Fatal("initialize storage quota", zap.Error(err))
	}
	storageBlobRepo := repository.NewStorageBlobRepository(gormDB)
	storageReconciler := service.NewStorageReconciler(
		logger,
		storageDependencies.reconciliation,
		storageBlobRepo,
		time.Duration(cfg.StorageStagingTTLSeconds)*time.Second,
	)
	reconciliationReport, reconciliationRan, err := reconcileStorageIfNeeded(
		context.Background(),
		*reconcileOnly,
		storageQuotaRepo,
		storageReconciler,
	)
	if err != nil {
		logger.Fatal("reconcile managed storage", zap.Error(err))
	}
	if !reconciliationRan {
		logger.Info("storage reconciliation already completed; skipping full filesystem scan")
	}
	if *reconcileOnly {
		logger.Info(
			"storage reconciliation command completed",
			zap.Int("tracked_blobs", reconciliationReport.TrackedBlobs),
			zap.Int("registered_orphans", reconciliationReport.RegisteredOrphans),
			zap.Int("missing_active", reconciliationReport.MissingActive),
		)
		return
	}
	if cleanupID := strings.TrimSpace(*orphanCleanupID); cleanupID != "" {
		if err := storageBlobRepo.ScheduleOrphanCleanup(context.Background(), cleanupID, time.Now().UTC()); err != nil {
			logger.Fatal("enqueue orphan cleanup", zap.String("blob_id", cleanupID), zap.Error(err))
		}
		logger.Info("orphan cleanup queued", zap.String("blob_id", cleanupID))
		return
	}

	blobLifecycle := service.NewBlobLifecycleService(
		logger,
		gormDB,
		storageDependencies.lifecycle,
		storageBlobRepo,
		uint64(cfg.ChunkSizeBytes),
	)
	blobLifecycle.SetStagingLease(time.Duration(cfg.StorageStagingTTLSeconds) * time.Second)
	cleanupWorker := service.NewStorageCleanupWorker(
		logger,
		storageDependencies.cleanup,
		storageBlobRepo,
		time.Duration(cfg.StorageStagingTTLSeconds)*time.Second,
	)
	runtimeMetrics := &service.RuntimeMetrics{}
	blobLifecycle.SetMetrics(runtimeMetrics)
	cleanupWorker.SetMetrics(runtimeMetrics)
	blobLifecycle.SetCleanupWake(cleanupWorker.Wake)
	blobLifecycle.SetCleanupRunOnce(func(ctx context.Context) error {
		return cleanupWorker.RunOnceAtDatabaseTime(ctx)
	})
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		cleanupWorker.Run(workerCtx)
	}()

	bucketService := service.NewBucketService(bucketRepo, objectRepo, recycleRepo, siteRepo, blobLifecycle)
	objectService := service.NewObjectService(gormDB, bucketRepo, objectRepo, recycleRepo, storageDependencies.reader, blobLifecycle)
	recycleBinService := service.NewRecycleBinService(gormDB, bucketRepo, objectRepo, recycleRepo, blobLifecycle)
	siteService := service.NewSiteService(bucketRepo, siteRepo, objectService)
	sitePublishService := service.NewSitePublishService(gormDB, objectRepo, siteRepo, blobLifecycle, siteService)
	signService := service.NewSignService(signing.NewSigner(cfg.SigningSecret), cfg.DefaultSignedURLTTLSeconds, cfg.MaxSignedURLTTLSeconds)
	systemStatsService := service.NewSystemStatsService(logger, storageQuotaService)

	router := handler.NewRouter(handler.Dependencies{
		Config:              cfg,
		Logger:              logger,
		DB:                  sqlDB,
		AuthValidator:       tokenValidator,
		RateLimitStore:      rateLimitStore,
		BucketService:       bucketService,
		ObjectService:       objectService,
		RecycleBinService:   recycleBinService,
		SiteService:         siteService,
		SitePublishService:  sitePublishService,
		SignService:         signService,
		SystemStatsService:  systemStatsService,
		StorageQuotaService: storageQuotaService,
		RuntimeMetrics:      runtimeMetrics,
		ReadinessCheck: func(ctx context.Context) error {
			return checkRuntimeReadiness(
				ctx,
				sqlDB,
				storageDependencies.readiness,
				storageDependencies.identity,
				storageQuotaRepo,
			)
		},
	})

	server := &http.Server{
		Addr:              cfg.AppAddr,
		Handler:           router,
		ReadHeaderTimeout: time.Duration(cfg.ReadHeaderTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info("http server started", zap.String("addr", cfg.AppAddr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("listen and serve", zap.Error(err))
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeoutSeconds)*time.Second)
	defer cancel()

	logger.Info("shutting down")
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown server", zap.Error(err))
	}
	stopWorker()
	select {
	case <-workerDone:
	case <-ctx.Done():
		logger.Warn("storage cleanup worker shutdown timed out", zap.Error(ctx.Err()))
	}
}

func buildLogger(cfg config.Config) (*zap.Logger, error) {
	if cfg.AppEnv == "development" {
		return zap.NewDevelopment()
	}

	return zap.NewProduction()
}

type storageReadinessChecker interface {
	CheckReady(context.Context) error
}

type storageIdentityProvider interface {
	Identity(context.Context) (string, error)
}

type storageReconciliationStateReader interface {
	Get(context.Context) (*model.SystemStorageQuota, error)
}

type storageReconciliationRunner interface {
	Reconcile(context.Context) (*service.StorageReconciliationReport, error)
}

type configuredFilesystemStore interface {
	service.BlobStore
	service.BlobReader
	service.StorageReconciliationStore
	storageReadinessChecker
	storageIdentityProvider
}

type runtimeStorageDependencies struct {
	lifecycle      service.BlobStore
	reader         service.BlobReader
	cleanup        service.StorageCleanupStore
	reconciliation service.StorageReconciliationStore
	readiness      storageReadinessChecker
	identity       storageIdentityProvider
}

func newRuntimeStorageDependencies(cfg config.Config) (runtimeStorageDependencies, error) {
	var store configuredFilesystemStore
	switch cfg.StorageMode {
	case config.StorageModeLocal:
		if err := os.MkdirAll(cfg.StorageRoot, 0o755); err != nil {
			return runtimeStorageDependencies{}, fmt.Errorf("create local storage root: %w", err)
		}
		store = storage.NewLocalStorage(cfg.StorageRoot)
	case config.StorageModeSharedFilesystem:
		sharedStore, err := storage.NewSharedFilesystemStorage(cfg.StorageRoot)
		if err != nil {
			return runtimeStorageDependencies{}, err
		}
		store = sharedStore
	default:
		return runtimeStorageDependencies{}, fmt.Errorf("unsupported storage mode %q", cfg.StorageMode)
	}

	return runtimeStorageDependencies{
		lifecycle:      store,
		reader:         store,
		cleanup:        store,
		reconciliation: store,
		readiness:      store,
		identity:       store,
	}, nil
}

func openSQLDB(cfg config.Config) (*sql.DB, error) {
	dsn, err := databaseDSNWithTimeouts(cfg)
	if err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(cfg.DatabaseMaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DatabaseMaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.DatabaseConnMaxLifetimeMinutes) * time.Minute)
	return sqlDB, nil
}

func databaseDSNWithTimeouts(cfg config.Config) (string, error) {
	dsn, err := mysql.ParseDSN(cfg.DatabaseDSN)
	if err != nil {
		return "", fmt.Errorf("parse database DSN: %w", err)
	}
	dsn.Timeout = time.Duration(cfg.DatabaseConnectTimeoutSeconds) * time.Second
	dsn.ReadTimeout = time.Duration(cfg.DatabaseReadTimeoutSeconds) * time.Second
	dsn.WriteTimeout = time.Duration(cfg.DatabaseWriteTimeoutSeconds) * time.Second
	return dsn.FormatDSN(), nil
}

func openGormDB(sqlDB *sql.DB) (*gorm.DB, error) {
	return gorm.Open(gormmysql.New(gormmysql.Config{
		Conn: sqlDB,
	}), &gorm.Config{})
}

func checkRuntimeReadiness(
	ctx context.Context,
	db *sql.DB,
	blobStore storageReadinessChecker,
	identityProvider storageIdentityProvider,
	quotaRepo *repository.StorageQuotaRepository,
) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	var dirty bool
	if err := db.QueryRowContext(ctx, "SELECT dirty FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&dirty); err != nil {
		return fmt.Errorf("check migration state: %w", err)
	}
	if dirty {
		return fmt.Errorf("database migration state is dirty")
	}

	if err := blobStore.CheckReady(ctx); err != nil {
		return fmt.Errorf("check storage root: %w", err)
	}
	quota, err := quotaRepo.Get(ctx)
	if err != nil {
		return fmt.Errorf("load storage reconciliation state: %w", err)
	}
	if quota.ReconciledAt == nil {
		return fmt.Errorf("storage reconciliation has not completed")
	}
	storageID, err := identityProvider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("read storage identity: %w", err)
	}
	if quota.StorageID == nil || *quota.StorageID != storageID {
		return fmt.Errorf("storage root identity does not match the database binding")
	}

	return nil
}

func reconcileStorageIfNeeded(
	ctx context.Context,
	force bool,
	stateReader storageReconciliationStateReader,
	reconciler storageReconciliationRunner,
) (*service.StorageReconciliationReport, bool, error) {
	quota, err := stateReader.Get(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("load storage reconciliation state: %w", err)
	}
	if !force && quota.ReconciledAt != nil && quota.StorageID != nil {
		return &service.StorageReconciliationReport{}, false, nil
	}
	report, err := reconciler.Reconcile(ctx)
	if err != nil {
		return report, true, err
	}
	return report, true, nil
}

func waitForDatabase(db *sql.DB, attempts int, interval time.Duration) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		lastErr = db.PingContext(ctx)
		cancel()
		if lastErr == nil {
			return nil
		}
		time.Sleep(interval)
	}

	return lastErr
}

type migrationRunner interface {
	Up() error
}

func runMigrations(db *sql.DB) (resultErr error) {
	absPath, err := filepath.Abs("migrations")
	if err != nil {
		return err
	}

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}

	driver, err := migratemysql.WithConnection(ctx, conn, &migratemysql.Config{
		// The MySQL driver uses GET_LOCK/RELEASE_LOCK when NoLock is false.
		NoLock: false,
	})
	if err != nil {
		return errors.Join(err, conn.Close())
	}

	migrator, err := migrate.NewWithDatabaseInstance(migrationSourceURL(absPath), "mysql", driver)
	if err != nil {
		return errors.Join(err, driver.Close())
	}
	defer func() {
		sourceErr, databaseErr := migrator.Close()
		if sourceErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close migration source: %w", sourceErr))
		}
		if databaseErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close migration connection: %w", databaseErr))
		}
	}()

	return applyMigrations(migrator)
}

func applyMigrations(migrator migrationRunner) error {
	err := migrator.Up()
	if err == nil || errors.Is(err, migrate.ErrNoChange) {
		return nil
	}

	var dirtyErr migrate.ErrDirty
	if errors.As(err, &dirtyErr) {
		return fmt.Errorf(
			"database migration version %d is dirty; repair or roll back the failed migration and resolve schema_migrations manually before restarting: %w",
			dirtyErr.Version,
			err,
		)
	}

	return err
}

func migrationSourceURL(path string) string {
	return (&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}).String()
}
