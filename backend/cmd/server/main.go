package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"go.uber.org/zap"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"

	"light-oss/backend/internal/config"
	"light-oss/backend/internal/handler"
	"light-oss/backend/internal/middleware"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/service"
	"light-oss/backend/internal/signing"
	"light-oss/backend/internal/storage"
)

func main() {
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

	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	if err := os.MkdirAll(cfg.StorageRoot, 0o755); err != nil {
		logger.Fatal("create storage root", zap.Error(err))
	}

	bootstrapDB, err := openSQLDB(cfg)
	if err != nil {
		logger.Fatal("open database", zap.Error(err))
	}
	defer func() {
		_ = bootstrapDB.Close()
	}()

	if err := waitForDatabase(bootstrapDB, 30, 2*time.Second); err != nil {
		logger.Fatal("wait for database", zap.Error(err))
	}

	if err := runMigrations(bootstrapDB); err != nil {
		logger.Fatal("run migrations", zap.Error(err))
	}

	runtimeDB, err := openSQLDB(cfg)
	if err != nil {
		logger.Fatal("open runtime database", zap.Error(err))
	}
	defer func() {
		_ = runtimeDB.Close()
	}()

	gormDB, err := gorm.Open(gormmysql.Open(cfg.DatabaseDSN), &gorm.Config{})
	if err != nil {
		logger.Fatal("open gorm database", zap.Error(err))
	}

	tokenValidator := middleware.NewTokenValidator(cfg.BearerTokens)
	bucketRepo := repository.NewBucketRepository(gormDB)
	objectRepo := repository.NewObjectRepository(gormDB)
	recycleRepo := repository.NewRecycleBinRepository(gormDB)
	siteRepo := repository.NewSiteRepository(gormDB)
	localStorage := storage.NewLocalStorage(cfg.StorageRoot)
	storageQuotaRepo := repository.NewStorageQuotaRepository(gormDB)
	storageQuotaService := service.NewStorageQuotaService(logger, cfg.StorageRoot, localStorage, objectRepo, recycleRepo, storageQuotaRepo)
	bucketService := service.NewBucketService(logger, gormDB, bucketRepo, objectRepo, recycleRepo, siteRepo, storageQuotaService)
	objectService := service.NewObjectService(gormDB, bucketRepo, objectRepo, recycleRepo, localStorage, storageQuotaService)
	recycleBinService := service.NewRecycleBinService(gormDB, bucketRepo, objectRepo, recycleRepo, storageQuotaService)
	siteService := service.NewSiteService(bucketRepo, siteRepo, objectService)
	sitePublishService := service.NewSitePublishService(gormDB, objectRepo, siteRepo, localStorage, storageQuotaService, siteService)
	signService := service.NewSignService(signing.NewSigner(cfg.SigningSecret), cfg.PublicBaseURL, cfg.DefaultSignedURLTTLSeconds, cfg.MaxSignedURLTTLSeconds)
	systemStatsService := service.NewSystemStatsService(logger, storageQuotaService)

	router := handler.NewRouter(handler.Dependencies{
		Config:              cfg,
		Logger:              logger,
		DB:                  runtimeDB,
		GormDB:              gormDB,
		AuthValidator:       tokenValidator,
		BucketService:       bucketService,
		ObjectService:       objectService,
		RecycleBinService:   recycleBinService,
		SiteService:         siteService,
		SitePublishService:  sitePublishService,
		SignService:         signService,
		SystemStatsService:  systemStatsService,
		StorageQuotaService: storageQuotaService,
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
}

func buildLogger(cfg config.Config) (*zap.Logger, error) {
	if cfg.AppEnv == "development" {
		return zap.NewDevelopment()
	}

	return zap.NewProduction()
}

func openSQLDB(cfg config.Config) (*sql.DB, error) {
	sqlDB, err := sql.Open("mysql", cfg.DatabaseDSN)
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(cfg.DatabaseMaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DatabaseMaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.DatabaseConnMaxLifetimeMinutes) * time.Minute)
	return sqlDB, nil
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

func runMigrations(db *sql.DB) error {
	absPath, err := filepath.Abs("migrations")
	if err != nil {
		return err
	}

	driver, err := migratemysql.WithInstance(db, &migratemysql.Config{})
	if err != nil {
		return err
	}

	migrator, err := migrate.NewWithDatabaseInstance(migrationSourceURL(absPath), "mysql", driver)
	if err != nil {
		return err
	}

	if err := migrator.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			return nil
		}

		var dirtyErr migrate.ErrDirty
		if errors.As(err, &dirtyErr) {
			if forceErr := migrator.Force(dirtyErr.Version); forceErr != nil {
				return forceErr
			}

			if retryErr := migrator.Up(); retryErr != nil && !errors.Is(retryErr, migrate.ErrNoChange) {
				return retryErr
			}

			return nil
		}

		return err
	}

	return nil
}

func migrationSourceURL(path string) string {
	return (&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}).String()
}
