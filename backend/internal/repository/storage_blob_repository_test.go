package repository

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"light-oss/backend/internal/model"
)

func TestStorageBlobRepositoryReserveIsAtomicUnderConcurrency(t *testing.T) {
	db, repo := newStorageBlobRepositoryTestDB(t, 50, 8)
	ctx := context.Background()
	now := time.Now().UTC()
	const writers = 10

	for i := 0; i < writers; i++ {
		blob := &model.StorageBlob{
			ID:          fmt.Sprintf("blob-%02d", i),
			StoragePath: fmt.Sprintf("objects/%02d.bin", i),
			Status:      model.StorageBlobStatusStaging,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := repo.CreateStaging(ctx, blob); err != nil {
			t.Fatalf("create staging blob %d: %v", i, err)
		}
	}

	start := make(chan struct{})
	results := make(chan error, writers)
	var waitGroup sync.WaitGroup
	for i := 0; i < writers; i++ {
		blobID := fmt.Sprintf("blob-%02d", i)
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			results <- repo.Reserve(ctx, blobID, 10)
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	successes := 0
	quotaFailures := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrStorageQuotaExceeded):
			quotaFailures++
		default:
			t.Fatalf("unexpected reserve error: %v", err)
		}
	}
	if successes != 5 || quotaFailures != 5 {
		t.Fatalf("reserve results: successes=%d quota_failures=%d, want 5 and 5", successes, quotaFailures)
	}

	var quota model.SystemStorageQuota
	if err := db.First(&quota, "id = ?", systemStorageQuotaRowID).Error; err != nil {
		t.Fatalf("load quota: %v", err)
	}
	if quota.ReservedBytes != 50 || quota.UsedBytes != 0 {
		t.Fatalf("quota = %+v, want reserved=50 used=0", quota)
	}

	var totalReserved uint64
	if err := db.Model(&model.StorageBlob{}).Select("COALESCE(SUM(size), 0)").Scan(&totalReserved).Error; err != nil {
		t.Fatalf("sum blob reservations: %v", err)
	}
	if totalReserved != quota.ReservedBytes {
		t.Fatalf("blob reservations = %d, quota reservation = %d", totalReserved, quota.ReservedBytes)
	}
}

func TestStorageBlobRepositoryCleanupLeaseCanBeTakenOverAfterExpiry(t *testing.T) {
	db, repo := newStorageBlobRepositoryTestDB(t, 100, 1)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	blob := model.StorageBlob{
		ID:          "pending-blob",
		StoragePath: "objects/pending.bin",
		Size:        10,
		Status:      model.StorageBlobStatusPendingDelete,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create blob: %v", err)
	}
	if err := db.Model(&model.SystemStorageQuota{}).
		Where("id = ?", systemStorageQuotaRowID).
		UpdateColumn("used_bytes", 10).Error; err != nil {
		t.Fatalf("set quota usage: %v", err)
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
	claimed, err := repo.ClaimCleanupJob(ctx, job.ID, "worker-one", now, leaseUntil)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v; want claimed", claimed, err)
	}
	claimed, err = repo.ClaimCleanupJob(ctx, job.ID, "worker-two", now.Add(30*time.Second), now.Add(90*time.Second))
	if err != nil {
		t.Fatalf("claim before expiry: %v", err)
	}
	if claimed {
		t.Fatal("second worker claimed an unexpired lease")
	}

	candidates, err := repo.ListClaimCandidates(ctx, now.Add(30*time.Second), 10)
	if err != nil {
		t.Fatalf("list candidates before expiry: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates before expiry = %d, want 0", len(candidates))
	}

	takeoverAt := leaseUntil.Add(time.Millisecond)
	claimed, err = repo.ClaimCleanupJob(ctx, job.ID, "worker-two", takeoverAt, takeoverAt.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim after expiry = %v, %v; want claimed", claimed, err)
	}
	if err := repo.CompleteCleanupJob(ctx, job.ID, "worker-one"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale owner completion error = %v, want record not found", err)
	}
	if err := repo.CompleteCleanupJob(ctx, job.ID, "worker-two"); err != nil {
		t.Fatalf("complete cleanup as takeover owner: %v", err)
	}

	if _, err := repo.FindByID(ctx, blob.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("find cleaned blob error = %v, want record not found", err)
	}
	count, err := repo.CleanupJobCount(ctx)
	if err != nil {
		t.Fatalf("count cleanup jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("cleanup job count = %d, want 0", count)
	}
	var quota model.SystemStorageQuota
	if err := db.First(&quota, "id = ?", systemStorageQuotaRowID).Error; err != nil {
		t.Fatalf("load quota: %v", err)
	}
	if quota.UsedBytes != 0 {
		t.Fatalf("used bytes after cleanup = %d, want 0", quota.UsedBytes)
	}
}

func TestStorageBlobRepositoryScheduleOrphanCleanupIsAtomicAndIdempotent(t *testing.T) {
	db, repo := newStorageBlobRepositoryTestDB(t, 100, 1)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	blob := model.StorageBlob{
		ID:          "orphan-cleanup",
		StoragePath: "objects/orphan-cleanup.bin",
		Size:        10,
		Status:      model.StorageBlobStatusOrphaned,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create orphaned blob: %v", err)
	}
	if err := db.Model(&model.SystemStorageQuota{}).
		Where("id = ?", systemStorageQuotaRowID).
		UpdateColumn("used_bytes", blob.Size).Error; err != nil {
		t.Fatalf("set quota usage: %v", err)
	}

	if err := repo.ScheduleOrphanCleanup(ctx, blob.ID, now); err != nil {
		t.Fatalf("schedule orphan cleanup: %v", err)
	}
	pending, err := repo.FindByID(ctx, blob.ID)
	if err != nil {
		t.Fatalf("load scheduled blob: %v", err)
	}
	if pending.Status != model.StorageBlobStatusPendingDelete || pending.RefCount != 0 {
		t.Fatalf("scheduled blob = %+v, want pending_delete ref_count=0", pending)
	}

	var job model.StorageCleanupJob
	if err := db.Where("blob_id = ?", blob.ID).First(&job).Error; err != nil {
		t.Fatalf("load scheduled cleanup job: %v", err)
	}
	claimed, err := repo.ClaimCleanupJob(ctx, job.ID, "worker", now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim cleanup job = %v, %v; want claimed", claimed, err)
	}

	retryAt := now.Add(2 * time.Minute)
	if err := repo.ScheduleOrphanCleanup(ctx, blob.ID, retryAt); err != nil {
		t.Fatalf("repeat orphan cleanup scheduling: %v", err)
	}
	var jobs []model.StorageCleanupJob
	if err := db.Where("blob_id = ?", blob.ID).Find(&jobs).Error; err != nil {
		t.Fatalf("list scheduled cleanup jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("cleanup jobs = %d, want 1", len(jobs))
	}
	job = jobs[0]
	if job.StoragePath != blob.StoragePath || !job.NextRetryAt.Equal(retryAt) || job.LeaseOwner != nil || job.LeaseExpiresAt != nil {
		t.Fatalf("refreshed cleanup job = %+v", job)
	}
	quota := loadStorageBlobRepositoryQuota(t, db)
	if quota.UsedBytes != blob.Size || quota.ReservedBytes != 0 {
		t.Fatalf("quota after scheduling cleanup = %+v, want used=%d", quota, blob.Size)
	}
}

func TestStorageBlobRepositoryScheduleOrphanCleanupRejectsOtherStatesAndReferences(t *testing.T) {
	db, repo := newStorageBlobRepositoryTestDB(t, 100, 1)
	ctx := context.Background()
	now := time.Now().UTC()
	blobs := []model.StorageBlob{
		{
			ID:          "active",
			StoragePath: "objects/active.bin",
			RefCount:    1,
			Status:      model.StorageBlobStatusActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "staging",
			StoragePath: "objects/staging.bin",
			Status:      model.StorageBlobStatusStaging,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "referenced-orphan",
			StoragePath: "objects/referenced-orphan.bin",
			RefCount:    1,
			Status:      model.StorageBlobStatusOrphaned,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
	if err := db.Create(&blobs).Error; err != nil {
		t.Fatalf("create rejected blobs: %v", err)
	}

	for _, blob := range blobs {
		if err := repo.ScheduleOrphanCleanup(ctx, blob.ID, now); err == nil {
			t.Fatalf("schedule blob %s in status %s with ref_count=%d unexpectedly succeeded", blob.ID, blob.Status, blob.RefCount)
		}
	}
	count, err := repo.CleanupJobCount(ctx)
	if err != nil {
		t.Fatalf("count cleanup jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("cleanup jobs = %d, want 0", count)
	}
}

func TestStorageBlobRepositoryMarkReconciliationStartedRejectsMissingQuotaRow(t *testing.T) {
	db, repo := newStorageBlobRepositoryTestDB(t, 100, 1)
	if err := db.Delete(&model.SystemStorageQuota{}, "id = ?", systemStorageQuotaRowID).Error; err != nil {
		t.Fatalf("delete quota row: %v", err)
	}

	err := repo.MarkReconciliationStarted(context.Background())
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("mark reconciliation started error = %v, want record not found", err)
	}
}

func TestStorageQuotaRepositoryBindsOnePersistentStorageIdentity(t *testing.T) {
	db, _ := newStorageBlobRepositoryTestDB(t, 100, 1)
	repo := NewStorageQuotaRepository(db)
	ctx := context.Background()
	if err := repo.BindStorageIdentity(ctx, "11111111-1111-4111-8111-111111111111"); err != nil {
		t.Fatalf("bind first storage identity: %v", err)
	}
	if err := repo.BindStorageIdentity(ctx, "11111111-1111-4111-8111-111111111111"); err != nil {
		t.Fatalf("repeat storage identity binding: %v", err)
	}
	if err := repo.BindStorageIdentity(ctx, "22222222-2222-4222-8222-222222222222"); err == nil {
		t.Fatal("different storage root identity unexpectedly replaced database binding")
	}
	quota, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("load bound storage identity: %v", err)
	}
	if quota.StorageID == nil || *quota.StorageID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("storage identity binding = %v", quota.StorageID)
	}
}

func newStorageBlobRepositoryTestDB(
	t *testing.T,
	maxBytes uint64,
	maxOpenConnections int,
) (*gorm.DB, *StorageBlobRepository) {
	t.Helper()

	databasePath := filepath.ToSlash(filepath.Join(t.TempDir(), "storage-blobs.db"))
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate",
		databasePath,
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConnections)
	sqlDB.SetMaxIdleConns(maxOpenConnections)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	if err := db.AutoMigrate(
		&model.SystemStorageQuota{},
		&model.StorageBlob{},
		&model.StorageCleanupJob{},
	); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&model.SystemStorageQuota{
		ID:        systemStorageQuotaRowID,
		MaxBytes:  maxBytes,
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create storage quota: %v", err)
	}

	return db, NewStorageBlobRepository(db)
}
