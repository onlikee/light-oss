package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"go.uber.org/zap"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"

	"light-oss/backend/internal/middleware"
	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/service"
	"light-oss/backend/internal/storage"
)

const (
	mysqlTestDSNEnvironment = "MYSQL_TEST_DSN"
	latestMigrationVersion  = 1
)

func TestMySQLMigrationsUpDownUp(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)

	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	if err := migrator.Down(); err != nil {
		t.Fatalf("migration down: %v", err)
	}
	if err := migrator.Up(); err != nil {
		t.Fatalf("second migration up: %v", err)
	}
	version, dirty, err := migrator.Version()
	if err != nil {
		t.Fatalf("migration version: %v", err)
	}
	if version != latestMigrationVersion || dirty {
		t.Fatalf("expected clean migration version %d, got version=%d dirty=%t", latestMigrationVersion, version, dirty)
	}
}

func TestMySQLConcurrentMigrationStartupUsesLock(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrators := []*migrate.Migrate{
		newMigrator(t, dsn),
		newMigrator(t, dsn),
	}
	start := make(chan struct{})
	results := make(chan error, len(migrators))
	var waitGroup sync.WaitGroup
	for _, migrator := range migrators {
		waitGroup.Add(1)
		go func(migrator *migrate.Migrate) {
			defer waitGroup.Done()
			<-start
			results <- migrator.Up()
		}(migrator)
	}
	close(start)
	waitGroup.Wait()
	close(results)

	for err := range results {
		if err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Fatalf("concurrent migration failed: %v", err)
		}
	}
	version, dirty, err := migrators[0].Version()
	if err != nil {
		t.Fatalf("migration version: %v", err)
	}
	if version != latestMigrationVersion || dirty {
		t.Fatalf("expected clean migration version %d, got version=%d dirty=%t", latestMigrationVersion, version, dirty)
	}
}

func TestMySQLStorageReconciliationLockKeepsQuotaModel(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	repo := repository.NewStorageBlobRepository(openGorm(t, dsn))
	storageID := uuid.NewString()
	if err := repo.WithReconciliationLock(context.Background(), time.Second, func(lockedRepo *repository.StorageBlobRepository) error {
		claimed, err := lockedRepo.ClaimStorageIdentity(context.Background(), storageID)
		if err != nil {
			return err
		}
		if !claimed {
			t.Fatalf("storage identity was not claimed")
		}
		return nil
	}); err != nil {
		t.Fatalf("claim storage identity while holding reconciliation lock: %v", err)
	}

	var quota model.SystemStorageQuota
	if err := openGorm(t, dsn).First(&quota, "id = ?", 1).Error; err != nil {
		t.Fatalf("load storage quota: %v", err)
	}
	if quota.StorageID == nil || *quota.StorageID != storageID {
		t.Fatalf("storage identity = %v, want %q", quota.StorageID, storageID)
	}
}

func TestMySQLAtomicQuotaReservationAcrossInstances(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	firstDB := openGorm(t, dsn)
	secondDB := openGorm(t, dsn)
	if err := firstDB.Model(&model.SystemStorageQuota{}).
		Where("id = ?", 1).
		Updates(map[string]any{"max_bytes": 100, "used_bytes": 0, "reserved_bytes": 0}).Error; err != nil {
		t.Fatalf("reset quota: %v", err)
	}

	blobs := []model.StorageBlob{
		{ID: uuid.NewString(), StoragePath: "objects/first.bin", Status: model.StorageBlobStatusStaging},
		{ID: uuid.NewString(), StoragePath: "objects/second.bin", Status: model.StorageBlobStatusStaging},
	}
	if err := firstDB.Create(&blobs).Error; err != nil {
		t.Fatalf("create staging blobs: %v", err)
	}

	repositories := []*repository.StorageBlobRepository{
		repository.NewStorageBlobRepository(firstDB),
		repository.NewStorageBlobRepository(secondDB),
	}
	start := make(chan struct{})
	errorsByInstance := make(chan error, len(repositories))
	var waitGroup sync.WaitGroup
	for index, blobRepo := range repositories {
		waitGroup.Add(1)
		go func(index int, blobRepo *repository.StorageBlobRepository) {
			defer waitGroup.Done()
			<-start
			errorsByInstance <- blobRepo.Reserve(context.Background(), blobs[index].ID, 80)
		}(index, blobRepo)
	}
	close(start)
	waitGroup.Wait()
	close(errorsByInstance)

	succeeded := 0
	rejected := 0
	for err := range errorsByInstance {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, repository.ErrStorageQuotaExceeded):
			rejected++
		default:
			t.Fatalf("unexpected reservation error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("expected one success and one quota rejection, got success=%d rejected=%d", succeeded, rejected)
	}

	quota, err := repository.NewStorageQuotaRepository(firstDB).Get(context.Background())
	if err != nil {
		t.Fatalf("load quota: %v", err)
	}
	if quota.UsedBytes+quota.ReservedBytes > quota.MaxBytes {
		t.Fatalf("quota exceeded atomically: used=%d reserved=%d max=%d", quota.UsedBytes, quota.ReservedBytes, quota.MaxBytes)
	}
}

func TestMySQLSharedRateLimitDoesNotScaleWithRouterCount(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	firstStore := middleware.NewMySQLRateLimitStore(openSQL(t, dsn), 10000)
	secondStore := middleware.NewMySQLRateLimitStore(openSQL(t, dsn), 10000)
	if allowed, err := firstStore.Allow(context.Background(), "startup-probe", 1, 1, time.Minute); err != nil {
		t.Fatalf("initialize shared rate limit store: %v", err)
	} else if !allowed {
		t.Fatal("expected a fresh shared rate limit bucket to allow its first request")
	}
	if _, err := openSQL(t, dsn).ExecContext(context.Background(), "DELETE FROM rate_limit_buckets"); err != nil {
		t.Fatalf("reset shared rate limit buckets: %v", err)
	}
	if _, err := openSQL(t, dsn).ExecContext(context.Background(), `
		UPDATE rate_limit_capacity
		SET entry_count = 0, expired_evictions = 0, capacity_rejections = 0
		WHERE id = 1`); err != nil {
		t.Fatalf("reset shared rate limit capacity: %v", err)
	}
	routers := []*gin.Engine{
		newSharedRateLimitRouter(middleware.NewRateLimiterWithStore("management", 0.000000001, 2, time.Hour, firstStore)),
		newSharedRateLimitRouter(middleware.NewRateLimiterWithStore("management", 0.000000001, 2, time.Hour, secondStore)),
	}

	start := make(chan struct{})
	statuses := make(chan int, 4)
	var waitGroup sync.WaitGroup
	for i := 0; i < 4; i++ {
		waitGroup.Add(1)
		go func(router *gin.Engine) {
			defer waitGroup.Done()
			<-start
			req := httptest.NewRequest(http.MethodGet, "/limited", nil)
			req.RemoteAddr = "192.0.2.10:1234"
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			statuses <- rec.Code
		}(routers[i%len(routers)])
	}
	close(start)
	waitGroup.Wait()
	close(statuses)

	allowed := 0
	rejected := 0
	for status := range statuses {
		switch status {
		case http.StatusNoContent:
			allowed++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Fatalf("unexpected shared limiter status: %d", status)
		}
	}
	if allowed != 2 || rejected != 2 {
		t.Fatalf("expected one shared burst of two across both routers, got allowed=%d rejected=%d", allowed, rejected)
	}

	var bucketCount int
	if err := openSQL(t, dsn).QueryRowContext(context.Background(), "SELECT COUNT(*) FROM rate_limit_buckets").Scan(&bucketCount); err != nil {
		t.Fatalf("count shared rate limit buckets: %v", err)
	}
	if bucketCount != 1 {
		t.Fatalf("expected one shared bucket, got %d", bucketCount)
	}
}

func TestMySQLSharedRateLimitRefillsAndResetsExpiredBucket(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	db := openSQL(t, dsn)
	store := middleware.NewMySQLRateLimitStore(db, 10000)
	ctx := context.Background()
	key := "management:identity:hashed-scope"
	for request := 1; request <= 3; request++ {
		allowed, err := store.Allow(ctx, key, 0.000000001, 2, time.Hour)
		if err != nil {
			t.Fatalf("consume request %d: %v", request, err)
		}
		if allowed != (request <= 2) {
			t.Fatalf("request %d allowed=%t, want %t", request, allowed, request <= 2)
		}
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE rate_limit_buckets
		SET
			tokens = 0,
			last_refill_at = TIMESTAMPADD(SECOND, -2, UTC_TIMESTAMP(6)),
			expires_at = TIMESTAMPADD(HOUR, 1, UTC_TIMESTAMP(6))`); err != nil {
		t.Fatalf("prepare refill state: %v", err)
	}
	if allowed, err := store.Allow(ctx, key, 1, 2, time.Hour); err != nil {
		t.Fatalf("consume refilled token: %v", err)
	} else if !allowed {
		t.Fatal("expected elapsed database time to refill the shared bucket")
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE rate_limit_buckets
		SET
			tokens = 0,
			last_refill_at = UTC_TIMESTAMP(6),
			expires_at = TIMESTAMPADD(SECOND, -1, UTC_TIMESTAMP(6))`); err != nil {
		t.Fatalf("prepare expired state: %v", err)
	}
	if allowed, err := store.Allow(ctx, key, 0.000000001, 2, time.Hour); err != nil {
		t.Fatalf("consume reset token: %v", err)
	} else if !allowed {
		t.Fatal("expected an expired shared bucket to reset to its configured burst")
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE rate_limit_buckets
		SET expires_at = TIMESTAMPADD(SECOND, -1, UTC_TIMESTAMP(6))`); err != nil {
		t.Fatalf("expire bucket for cleanup: %v", err)
	}
	if deleted, err := store.CleanupExpired(ctx); err != nil {
		t.Fatalf("cleanup expired bucket: %v", err)
	} else if deleted != 1 {
		t.Fatalf("deleted expired buckets=%d, want 1", deleted)
	}
}

func TestMySQLSharedRateLimitHasGlobalCapacityAndExpiry(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	ctx := context.Background()
	stores := []*middleware.MySQLRateLimitStore{
		middleware.NewMySQLRateLimitStore(openSQL(t, dsn), 2),
		middleware.NewMySQLRateLimitStore(openSQL(t, dsn), 2),
	}
	start := make(chan struct{})
	results := make(chan bool, 3)
	errors := make(chan error, 3)
	var waitGroup sync.WaitGroup
	for request := range 3 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			allowed, err := stores[request%len(stores)].Allow(
				ctx,
				fmt.Sprintf("global-ip:ip:192.0.2.%d", request+1),
				1,
				1,
				time.Hour,
			)
			if err != nil {
				errors <- err
				return
			}
			results <- allowed
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatalf("bounded shared limiter: %v", err)
	}
	allowedCount := 0
	for allowed := range results {
		if allowed {
			allowedCount++
		}
	}
	if allowedCount != 2 {
		t.Fatalf("fresh keys allowed=%d, want exactly 2", allowedCount)
	}

	stats, err := stores[0].Stats(ctx)
	if err != nil {
		t.Fatalf("read shared limiter stats: %v", err)
	}
	if stats.Entries != 2 || stats.MaxEntries != 2 || stats.CapacityRejections != 1 {
		t.Fatalf("shared limiter stats = %+v, want entries=2 max=2 capacity_rejections=1", stats)
	}

	db := openSQL(t, dsn)
	if _, err := db.ExecContext(ctx, `
		UPDATE rate_limit_buckets
		SET expires_at = TIMESTAMPADD(SECOND, -1, UTC_TIMESTAMP(6))`); err != nil {
		t.Fatalf("expire bounded buckets: %v", err)
	}
	if deleted, err := stores[1].CleanupExpired(ctx); err != nil {
		t.Fatalf("cleanup bounded buckets: %v", err)
	} else if deleted != 2 {
		t.Fatalf("deleted bounded buckets=%d, want 2", deleted)
	}
	if allowed, err := stores[0].Allow(ctx, "global-ip:ip:192.0.2.4", 1, 1, time.Hour); err != nil {
		t.Fatalf("allow after expiry cleanup: %v", err)
	} else if !allowed {
		t.Fatal("expected capacity released by expiry cleanup")
	}
	stats, err = stores[1].Stats(ctx)
	if err != nil {
		t.Fatalf("read shared limiter stats after cleanup: %v", err)
	}
	if stats.Entries != 1 || stats.ExpiredEvictions != 2 {
		t.Fatalf("shared limiter stats after cleanup = %+v, want entries=1 expired_evictions=2", stats)
	}
}

func TestMySQLStorageBlobMigrationBackfillsSharedReferences(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Steps(6); err != nil {
		t.Fatalf("migrate through version 6: %v", err)
	}

	db := openGorm(t, dsn)
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := db.Create(&model.Bucket{Name: "backfill", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	sharedPath := "objects/shared-backfill.bin"
	if err := db.Create(&model.Object{
		BucketName:       "backfill",
		ObjectKey:        "active.txt",
		OriginalFilename: "active.txt",
		StoragePath:      sharedPath,
		Size:             7,
		ContentType:      "text/plain",
		ETag:             "active-etag",
		Visibility:       model.VisibilityPrivate,
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create active object: %v", err)
	}
	if err := db.Omit("DeleteGroupID").Create(&model.RecycleBinObject{
		BucketName:       "backfill",
		ObjectKey:        "deleted.txt",
		OriginalFilename: "deleted.txt",
		StoragePath:      sharedPath,
		Size:             7,
		ContentType:      "text/plain",
		ETag:             "deleted-etag",
		Visibility:       model.VisibilityPrivate,
		CreatedAt:        now,
		DeletedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create recycle-bin object: %v", err)
	}

	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrate to version 7: %v", err)
	}
	var blob model.StorageBlob
	if err := db.Select("id", "storage_path", "size", "ref_count", "status", "created_at", "updated_at").
		Where("storage_path = ?", sharedPath).
		First(&blob).Error; err != nil {
		t.Fatalf("load backfilled blob: %v", err)
	}
	if blob.Status != model.StorageBlobStatusActive || blob.Size != 7 || blob.RefCount != 2 {
		t.Fatalf("backfilled blob = %+v, want active size=7 ref_count=2", blob)
	}
	var quota struct {
		UsedBytes     uint64
		ReservedBytes uint64
		ReconciledAt  *time.Time
	}
	if err := db.Raw(`
		SELECT used_bytes, reserved_bytes, reconciled_at
		FROM system_storage_quotas
		WHERE id = 1`).Scan(&quota).Error; err != nil {
		t.Fatalf("load backfilled quota: %v", err)
	}
	if quota.UsedBytes != 7 || quota.ReservedBytes != 0 || quota.ReconciledAt != nil {
		t.Fatalf("backfilled quota = %+v, want used=7 reserved=0 unreconciled", quota)
	}
}

func TestMySQLStorageReconciliationAdvisoryLockSerializesInstances(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	firstRepo := repository.NewStorageBlobRepository(openGorm(t, dsn))
	secondRepo := repository.NewStorageBlobRepository(openGorm(t, dsn))
	firstAcquired := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- firstRepo.WithReconciliationLock(context.Background(), 2*time.Second, func(*repository.StorageBlobRepository) error {
			close(firstAcquired)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstAcquired:
	case <-time.After(5 * time.Second):
		t.Fatal("first reconciliation lock was not acquired")
	}

	secondAcquired := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		secondResult <- secondRepo.WithReconciliationLock(context.Background(), 2*time.Second, func(*repository.StorageBlobRepository) error {
			close(secondAcquired)
			return nil
		})
	}()
	select {
	case <-secondAcquired:
		t.Fatal("second instance acquired reconciliation lock while first still held it")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatalf("first reconciliation lock: %v", err)
	}
	select {
	case <-secondAcquired:
	case <-time.After(5 * time.Second):
		t.Fatal("second reconciliation lock was not acquired after release")
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("second reconciliation lock: %v", err)
	}
}

func TestMySQLStorageReconciliationAdvisoryLockReleasesAfterContextCancellation(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	firstRepo := repository.NewStorageBlobRepository(openGorm(t, dsn))
	secondRepo := repository.NewStorageBlobRepository(openGorm(t, dsn))
	ctx, cancel := context.WithCancel(context.Background())
	firstErr := firstRepo.WithReconciliationLock(ctx, 2*time.Second, func(*repository.StorageBlobRepository) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(firstErr, context.Canceled) {
		t.Fatalf("first reconciliation error = %v, want context canceled", firstErr)
	}

	acquired := false
	if err := secondRepo.WithReconciliationLock(context.Background(), time.Second, func(*repository.StorageBlobRepository) error {
		acquired = true
		return nil
	}); err != nil {
		t.Fatalf("acquire reconciliation lock after canceled owner: %v", err)
	}
	if !acquired {
		t.Fatal("second connection did not acquire reconciliation lock after canceled owner")
	}
}

func TestMySQLSharedFilesystemCrossInstanceLifecycleAndTakeover(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	sharedRoot := t.TempDir()
	first := newSharedFilesystemServiceGraph(t, openGorm(t, dsn), sharedRoot)
	second := newSharedFilesystemServiceGraph(t, openGorm(t, dsn), sharedRoot)
	ctx := context.Background()
	if err := first.db.Create(&model.Bucket{Name: "shared"}).Error; err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	original, err := first.objects.Upload(ctx, service.UploadObjectInput{
		BucketName:       "shared",
		ObjectKey:        "cross-instance.txt",
		Visibility:       string(model.VisibilityPrivate),
		OriginalFilename: "cross-instance.txt",
		ContentType:      "text/plain",
		Body:             strings.NewReader("first-instance"),
	})
	if err != nil {
		t.Fatalf("first instance upload: %v", err)
	}
	assertSharedObjectContents(t, second.objects, "first-instance")

	replacement, err := second.objects.Upload(ctx, service.UploadObjectInput{
		BucketName:       "shared",
		ObjectKey:        "cross-instance.txt",
		Visibility:       string(model.VisibilityPrivate),
		AllowOverwrite:   true,
		OriginalFilename: "cross-instance.txt",
		ContentType:      "text/plain",
		Body:             strings.NewReader("second-instance"),
	})
	if err != nil {
		t.Fatalf("second instance overwrite: %v", err)
	}
	if replacement.StoragePath == original.StoragePath {
		t.Fatalf("overwrite reused storage path %q", replacement.StoragePath)
	}
	assertSharedObjectContents(t, first.objects, "second-instance")

	if err := second.cleanup.RunOnce(ctx, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("second instance cleans replaced blob: %v", err)
	}
	if _, err := first.store.Stat(original.StoragePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaced physical blob stat error = %v, want not exist", err)
	}

	if err := first.objects.Delete(ctx, "shared", "cross-instance.txt"); err != nil {
		t.Fatalf("first instance moves object to recycle bin: %v", err)
	}
	var recycled model.RecycleBinObject
	if err := second.db.Where("bucket_name = ? AND object_key = ?", "shared", "cross-instance.txt").First(&recycled).Error; err != nil {
		t.Fatalf("second instance reads recycle metadata: %v", err)
	}
	deleted, err := first.recycle.DeleteObjects(ctx, []uint64{recycled.ID})
	if err != nil {
		t.Fatalf("first instance permanently deletes object: %v", err)
	}
	if deleted.DeletedCount != 1 || deleted.FailedCount != 0 {
		t.Fatalf("permanent delete result = %+v", deleted)
	}

	claimNow := time.Now().UTC().Add(time.Second)
	jobs, err := first.blobs.ListClaimCandidates(ctx, claimNow, 10)
	if err != nil {
		t.Fatalf("list cleanup candidates: %v", err)
	}
	if len(jobs) != 1 || jobs[0].StoragePath != replacement.StoragePath {
		t.Fatalf("cleanup candidates = %+v, want replacement path %q", jobs, replacement.StoragePath)
	}
	// Claiming without processing simulates the first instance exiting after it
	// acquired the database lease.
	leaseUntil := claimNow.Add(10 * time.Millisecond)
	claimed, err := first.blobs.ClaimCleanupJob(ctx, jobs[0].ID, "exited-instance", claimNow, leaseUntil)
	if err != nil || !claimed {
		t.Fatalf("first instance claim cleanup job: claimed=%t err=%v", claimed, err)
	}
	if err := second.cleanup.RunOnce(ctx, leaseUntil.Add(time.Second)); err != nil {
		t.Fatalf("second instance takes over expired cleanup lease: %v", err)
	}
	if _, err := second.store.Stat(replacement.StoragePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted physical blob stat error = %v, want not exist", err)
	}
	if count, err := second.blobs.CleanupJobCount(ctx); err != nil || count != 0 {
		t.Fatalf("cleanup backlog after takeover = %d, err=%v", count, err)
	}

	// Staging without publishing simulates the first instance exiting before its
	// metadata transaction starts.
	abandonedCtx, cancelAbandoned := context.WithCancel(ctx)
	abandoned, err := first.lifecycle.Stage(abandonedCtx, bytes.NewBufferString("abandoned-staging"))
	if err != nil {
		t.Fatalf("first instance stages abandoned blob: %v", err)
	}
	cancelAbandoned()
	recoveryNow := time.Now().UTC().Truncate(time.Millisecond)
	if err := first.db.Model(&model.StorageBlob{}).
		Where("id = ?", abandoned.ID).
		Updates(map[string]any{
			"created_at":               recoveryNow.Add(-2 * time.Second),
			"staging_lease_expires_at": recoveryNow.Add(-time.Second),
		}).Error; err != nil {
		t.Fatalf("age abandoned staging blob: %v", err)
	}
	if err := second.cleanup.RunOnce(ctx, recoveryNow); err != nil {
		t.Fatalf("second instance recovers abandoned staging: %v", err)
	}
	if _, err := second.store.Stat(abandoned.StoragePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned physical blob stat error = %v, want not exist", err)
	}
	var remainingAbandoned int64
	if err := second.db.Model(&model.StorageBlob{}).Where("id = ?", abandoned.ID).Count(&remainingAbandoned).Error; err != nil {
		t.Fatalf("count abandoned blob ledger rows: %v", err)
	}
	if remainingAbandoned != 0 {
		t.Fatalf("abandoned blob ledger rows = %d, want 0", remainingAbandoned)
	}
}

func TestMySQLCleanupLeaseRenewalTreatsUnchangedTimestampAsOwned(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	db := openGorm(t, dsn)
	repo := repository.NewStorageBlobRepository(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	blob := model.StorageBlob{
		ID:          uuid.NewString(),
		StoragePath: "objects/lease-renewal.bin",
		Size:        1,
		Status:      model.StorageBlobStatusPendingDelete,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create pending-delete blob: %v", err)
	}
	job := model.StorageCleanupJob{
		BlobID:      blob.ID,
		StoragePath: blob.StoragePath,
		NextRetryAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create cleanup job: %v", err)
	}

	leaseUntil := now.Add(time.Minute)
	claimed, err := repo.ClaimCleanupJob(ctx, job.ID, "worker", now, leaseUntil)
	if err != nil || !claimed {
		t.Fatalf("claim cleanup job: claimed=%t err=%v", claimed, err)
	}
	renewed, err := repo.RenewCleanupJobLease(ctx, job.ID, "worker", leaseUntil)
	if err != nil || !renewed {
		t.Fatalf("renew unchanged cleanup lease: renewed=%t err=%v", renewed, err)
	}
}

func TestMySQLStorageIdentityRejectsDifferentSharedRoot(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	ctx := context.Background()
	quotaRepo := repository.NewStorageQuotaRepository(openGorm(t, dsn))
	firstStore, err := storage.NewSharedFilesystemStorage(t.TempDir())
	if err != nil {
		t.Fatalf("open first shared root: %v", err)
	}
	firstID, err := firstStore.Identity(ctx)
	if err != nil {
		t.Fatalf("create first shared root identity: %v", err)
	}
	if err := quotaRepo.BindStorageIdentity(ctx, firstID); err != nil {
		t.Fatalf("bind first shared root identity: %v", err)
	}

	secondStore, err := storage.NewSharedFilesystemStorage(t.TempDir())
	if err != nil {
		t.Fatalf("open second shared root: %v", err)
	}
	secondID, err := secondStore.Identity(ctx)
	if err != nil {
		t.Fatalf("create second shared root identity: %v", err)
	}
	if err := quotaRepo.BindStorageIdentity(ctx, secondID); err == nil {
		t.Fatal("different shared root unexpectedly matched database storage identity")
	}
}

func TestMySQLStagingHeartbeatPreventsCrossInstanceCleanup(t *testing.T) {
	dsn := newIsolatedMySQLDatabase(t)
	migrator := newMigrator(t, dsn)
	if err := migrator.Up(); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	sharedRoot := t.TempDir()
	first := newSharedFilesystemServiceGraph(t, openGorm(t, dsn), sharedRoot)
	second := newSharedFilesystemServiceGraph(t, openGorm(t, dsn), sharedRoot)
	const stagingLease = 500 * time.Millisecond
	first.lifecycle.SetStagingLease(stagingLease)
	second.cleanup = service.NewStorageCleanupWorker(zap.NewNop(), second.store, second.blobs, stagingLease)
	if err := first.db.Create(&model.Bucket{Name: "heartbeat"}).Error; err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	reader := newBlockingMySQLUploadReader()
	defer reader.Release()
	type uploadResult struct {
		object *model.Object
		err    error
	}
	result := make(chan uploadResult, 1)
	go func() {
		object, err := first.objects.Upload(context.Background(), service.UploadObjectInput{
			BucketName:       "heartbeat",
			ObjectKey:        "slow.txt",
			Visibility:       string(model.VisibilityPrivate),
			OriginalFilename: "slow.txt",
			ContentType:      "text/plain",
			Body:             reader,
		})
		result <- uploadResult{object: object, err: err}
	}()

	select {
	case <-reader.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not reach the blocked reader")
	}
	time.Sleep(2 * stagingLease)
	if err := second.cleanup.RunOnceAtDatabaseTime(context.Background()); err != nil {
		t.Fatalf("second instance cleanup while upload is active: %v", err)
	}
	var stagingCount int64
	if err := second.db.Model(&model.StorageBlob{}).
		Where("status = ?", model.StorageBlobStatusStaging).
		Count(&stagingCount).Error; err != nil {
		t.Fatalf("count active staging blobs: %v", err)
	}
	if stagingCount != 1 {
		t.Fatalf("active staging blobs=%d, want 1", stagingCount)
	}
	if backlog, err := second.blobs.CleanupJobCount(context.Background()); err != nil || backlog != 0 {
		t.Fatalf("cleanup backlog while upload is active=%d, err=%v", backlog, err)
	}

	reader.Release()
	select {
	case upload := <-result:
		if upload.err != nil {
			t.Fatalf("finish slow upload: %v", upload.err)
		}
		if upload.object == nil || upload.object.ObjectKey != "slow.txt" {
			t.Fatalf("uploaded object = %+v", upload.object)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("slow upload did not finish")
	}
	opened, body, err := second.objects.Open(context.Background(), "heartbeat", "slow.txt")
	if err != nil {
		t.Fatalf("open slow upload from second instance: %v", err)
	}
	contents, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read slow upload: read=%v close=%v", readErr, closeErr)
	}
	if opened.ObjectKey != "slow.txt" || string(contents) != "slow-upload" {
		t.Fatalf("slow upload contents=%q object=%+v", contents, opened)
	}
}

type blockingMySQLUploadReader struct {
	delivered   bool
	blocked     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func newBlockingMySQLUploadReader() *blockingMySQLUploadReader {
	return &blockingMySQLUploadReader{
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockingMySQLUploadReader) Read(buffer []byte) (int, error) {
	if !r.delivered {
		r.delivered = true
		return copy(buffer, "slow-upload"), nil
	}
	close(r.blocked)
	<-r.release
	return 0, io.EOF
}

func (r *blockingMySQLUploadReader) Release() {
	r.releaseOnce.Do(func() { close(r.release) })
}

type sharedFilesystemServiceGraph struct {
	db        *gorm.DB
	store     *storage.SharedFilesystemStorage
	objects   *service.ObjectService
	recycle   *service.RecycleBinService
	lifecycle *service.BlobLifecycleService
	cleanup   *service.StorageCleanupWorker
	blobs     *repository.StorageBlobRepository
}

func newSharedFilesystemServiceGraph(t *testing.T, db *gorm.DB, root string) *sharedFilesystemServiceGraph {
	t.Helper()
	store, err := storage.NewSharedFilesystemStorage(root)
	if err != nil {
		t.Fatalf("open shared filesystem store: %v", err)
	}
	bucketRepo := repository.NewBucketRepository(db)
	objectRepo := repository.NewObjectRepository(db)
	recycleRepo := repository.NewRecycleBinRepository(db)
	blobRepo := repository.NewStorageBlobRepository(db)
	lifecycle := service.NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 1024)

	return &sharedFilesystemServiceGraph{
		db:        db,
		store:     store,
		objects:   service.NewObjectService(db, bucketRepo, objectRepo, recycleRepo, store, lifecycle),
		recycle:   service.NewRecycleBinService(db, bucketRepo, objectRepo, recycleRepo, lifecycle),
		lifecycle: lifecycle,
		cleanup:   service.NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Second),
		blobs:     blobRepo,
	}
}

func assertSharedObjectContents(t *testing.T, objects *service.ObjectService, want string) {
	t.Helper()
	_, reader, err := objects.Open(context.Background(), "shared", "cross-instance.txt")
	if err != nil {
		t.Fatalf("open cross-instance object: %v", err)
	}
	contents, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read cross-instance object: read=%v close=%v", err, closeErr)
	}
	if string(contents) != want {
		t.Fatalf("cross-instance object contents = %q, want %q", contents, want)
	}
}

func newIsolatedMySQLDatabase(t testing.TB) string {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv(mysqlTestDSNEnvironment))
	if baseDSN == "" {
		t.Skipf("%s is not configured", mysqlTestDSNEnvironment)
	}

	parsed, err := mysql.ParseDSN(baseDSN)
	if err != nil {
		t.Fatalf("parse MySQL test DSN: %v", err)
	}
	parsed.MultiStatements = true
	parsed.ParseTime = true
	parsed.DBName = ""
	adminDB, err := sql.Open("mysql", parsed.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL admin connection: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	if err := adminDB.PingContext(context.Background()); err != nil {
		t.Fatalf("ping MySQL: %v", err)
	}
	var serverVersion string
	if err := adminDB.QueryRowContext(context.Background(), "SELECT VERSION()").Scan(&serverVersion); err != nil {
		t.Fatalf("read MySQL version: %v", err)
	}
	if !strings.HasPrefix(serverVersion, "8.") {
		t.Fatalf("MySQL 8.x is required for integration tests, got %s", serverVersion)
	}

	databaseName := "light_oss_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.ExecContext(context.Background(), "CREATE DATABASE `"+databaseName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatalf("create isolated database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+databaseName+"`"); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
	})

	parsed.DBName = databaseName
	return parsed.FormatDSN()
}

func newMigrator(t testing.TB, dsn string) *migrate.Migrate {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	connection, err := db.Conn(context.Background())
	if err != nil {
		_ = db.Close()
		t.Fatalf("open migration connection: %v", err)
	}
	driver, err := migratemysql.WithConnection(context.Background(), connection, &migratemysql.Config{NoLock: false})
	if err != nil {
		_ = connection.Close()
		_ = db.Close()
		t.Fatalf("create migration driver: %v", err)
	}

	migrationsPath, err := filepath.Abs(filepath.Join("..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations path: %v", err)
	}
	sourceURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(migrationsPath)}).String()
	migrator, err := migrate.NewWithDatabaseInstance(sourceURL, "mysql", driver)
	if err != nil {
		_ = driver.Close()
		_ = db.Close()
		t.Fatalf("create migrator: %v", err)
	}
	t.Cleanup(func() {
		sourceErr, databaseErr := migrator.Close()
		if sourceErr != nil {
			t.Errorf("close migration source: %v", sourceErr)
		}
		if databaseErr != nil {
			t.Errorf("close migration database: %v", databaseErr)
		}
		_ = db.Close()
	})
	return migrator
}

func openGorm(t testing.TB, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("load GORM sql.DB: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close GORM database: %v", err)
		}
	})
	return db
}

func openSQL(t testing.TB, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close MySQL database: %v", err)
		}
	})
	return db
}

func newSharedRateLimitRouter(limiter *middleware.RateLimiter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	_ = router.SetTrustedProxies(nil)
	router.Use(limiter.LimitByClientIP())
	router.GET("/limited", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return router
}
