package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
)

func TestStorageBlobRepositoryCreateAndActivateBatchAccountsAtomically(t *testing.T) {
	db, repo := newStorageBlobRepositoryTestDB(t, 100, 1)
	ctx := context.Background()
	blobs := make([]model.StorageBlob, 0, 3)
	activations := make([]StorageBlobActivation, 0, 3)
	for index, reservedSize := range []uint64{8, 12, 1} {
		blobID := fmt.Sprintf("batch-%d", index)
		stagingPath := fmt.Sprintf("staging/%d.tmp", index)
		blobs = append(blobs, model.StorageBlob{
			ID:          blobID,
			StoragePath: fmt.Sprintf("objects/%d.bin", index),
			StagingPath: &stagingPath,
			Size:        reservedSize,
			Status:      model.StorageBlobStatusStaging,
		})
		activations = append(activations, StorageBlobActivation{
			ID:         blobID,
			ActualSize: []uint64{5, 12, 0}[index],
		})
	}

	if err := repo.CreateStagingBatch(ctx, blobs, 2); err != nil {
		t.Fatalf("create staging batch: %v", err)
	}
	quota := loadStorageBlobRepositoryQuota(t, db)
	if quota.ReservedBytes != 21 || quota.UsedBytes != 0 {
		t.Fatalf("quota after reservation = %+v, want reserved=21 used=0", quota)
	}

	if err := repo.Transaction(ctx, func(txRepo *StorageBlobRepository) error {
		return txRepo.ActivateStagingBatch(ctx, activations, 2)
	}); err != nil {
		t.Fatalf("activate staging batch: %v", err)
	}
	quota = loadStorageBlobRepositoryQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 17 {
		t.Fatalf("quota after activation = %+v, want reserved=0 used=17", quota)
	}

	var activeCount int64
	if err := db.Model(&model.StorageBlob{}).
		Where("status = ? AND ref_count = ? AND staging_path IS NULL", model.StorageBlobStatusActive, 1).
		Count(&activeCount).Error; err != nil {
		t.Fatalf("count active blobs: %v", err)
	}
	if activeCount != 3 {
		t.Fatalf("active blobs = %d, want 3", activeCount)
	}
}

func TestStorageBlobRepositoryCreateBatchQuotaFailureRollsBackEveryRow(t *testing.T) {
	db, repo := newStorageBlobRepositoryTestDB(t, 9, 1)
	ctx := context.Background()
	firstStagingPath := "staging/first.tmp"
	secondStagingPath := "staging/second.tmp"
	blobs := []model.StorageBlob{
		{ID: "first", StoragePath: "objects/first.bin", StagingPath: &firstStagingPath, Size: 5, Status: model.StorageBlobStatusStaging},
		{ID: "second", StoragePath: "objects/second.bin", StagingPath: &secondStagingPath, Size: 5, Status: model.StorageBlobStatusStaging},
	}

	err := repo.CreateStagingBatch(ctx, blobs, 100)
	if !errors.Is(err, ErrStorageQuotaExceeded) {
		t.Fatalf("create staging batch error = %v, want quota exceeded", err)
	}

	var blobCount int64
	if err := db.Model(&model.StorageBlob{}).Count(&blobCount).Error; err != nil {
		t.Fatalf("count storage blobs: %v", err)
	}
	if blobCount != 0 {
		t.Fatalf("storage blob rows = %d, want 0", blobCount)
	}
	quota := loadStorageBlobRepositoryQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 0 {
		t.Fatalf("quota after failed reservation = %+v, want zero usage", quota)
	}
}

func TestStorageBlobRepositoryCreateStagingBatchIsAtomicUnderConcurrency(t *testing.T) {
	db, repo := newStorageBlobRepositoryTestDB(t, 40, 8)
	ctx := context.Background()
	const candidates = 4

	start := make(chan struct{})
	results := make(chan error, candidates)
	var waitGroup sync.WaitGroup
	for batchIndex := 0; batchIndex < candidates; batchIndex++ {
		batchIndex := batchIndex
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			firstStagingPath := fmt.Sprintf("staging/%d-first.tmp", batchIndex)
			secondStagingPath := fmt.Sprintf("staging/%d-second.tmp", batchIndex)
			results <- repo.CreateStagingBatch(ctx, []model.StorageBlob{
				{
					ID:          fmt.Sprintf("batch-%d-first", batchIndex),
					StoragePath: fmt.Sprintf("objects/%d-first.bin", batchIndex),
					StagingPath: &firstStagingPath,
					Size:        10,
					Status:      model.StorageBlobStatusStaging,
				},
				{
					ID:          fmt.Sprintf("batch-%d-second", batchIndex),
					StoragePath: fmt.Sprintf("objects/%d-second.bin", batchIndex),
					StagingPath: &secondStagingPath,
					Size:        10,
					Status:      model.StorageBlobStatusStaging,
				},
			}, 100)
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
			t.Fatalf("unexpected batch reservation error: %v", err)
		}
	}
	if successes != 2 || quotaFailures != 2 {
		t.Fatalf("batch reservation results: successes=%d quota_failures=%d, want 2 and 2", successes, quotaFailures)
	}
	quota := loadStorageBlobRepositoryQuota(t, db)
	if quota.ReservedBytes != 40 || quota.UsedBytes != 0 {
		t.Fatalf("quota after concurrent batches = %+v, want reserved=40 used=0", quota)
	}
	var blobCount int64
	if err := db.Model(&model.StorageBlob{}).Count(&blobCount).Error; err != nil {
		t.Fatalf("count storage blobs: %v", err)
	}
	if blobCount != 4 {
		t.Fatalf("storage blob rows = %d, want 4 from two complete batches", blobCount)
	}
}

func TestStorageBlobRepositoryReleaseReferencesBatchHandlesMixedCountsAndDuplicatePaths(t *testing.T) {
	db, repo := newStorageBlobRepositoryTestDB(t, 100, 1)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	blobs := []model.StorageBlob{
		{
			ID:          "remaining",
			StoragePath: "objects/remaining.bin",
			Size:        10,
			RefCount:    3,
			Status:      model.StorageBlobStatusActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "duplicate-release",
			StoragePath: "objects/duplicate-release.bin",
			Size:        20,
			RefCount:    2,
			Status:      model.StorageBlobStatusActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "pending-delete",
			StoragePath: "objects/pending-delete.bin",
			Size:        30,
			RefCount:    1,
			Status:      model.StorageBlobStatusActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
	if err := db.Create(&blobs).Error; err != nil {
		t.Fatalf("create active blobs: %v", err)
	}
	if err := db.Model(&model.SystemStorageQuota{}).
		Where("id = ?", systemStorageQuotaRowID).
		UpdateColumn("used_bytes", 60).Error; err != nil {
		t.Fatalf("set used bytes: %v", err)
	}

	err := repo.Transaction(ctx, func(txRepo *StorageBlobRepository) error {
		return txRepo.ReleaseReferences(ctx, []string{
			blobs[0].StoragePath,
			blobs[1].StoragePath,
			blobs[2].StoragePath,
			blobs[1].StoragePath,
		}, now, 2)
	})
	if err != nil {
		t.Fatalf("release reference batch: %v", err)
	}

	remaining, err := repo.FindByID(ctx, blobs[0].ID)
	if err != nil {
		t.Fatalf("find remaining blob: %v", err)
	}
	if remaining.RefCount != 2 || remaining.Status != model.StorageBlobStatusActive {
		t.Fatalf("remaining blob = %+v, want active ref_count=2", remaining)
	}
	for _, expected := range blobs[1:] {
		pending, err := repo.FindByID(ctx, expected.ID)
		if err != nil {
			t.Fatalf("find pending blob %s: %v", expected.ID, err)
		}
		if pending.RefCount != 0 || pending.Status != model.StorageBlobStatusPendingDelete {
			t.Fatalf("pending blob = %+v, want pending_delete ref_count=0", pending)
		}
	}

	var jobs []model.StorageCleanupJob
	if err := db.Order("blob_id ASC").Find(&jobs).Error; err != nil {
		t.Fatalf("list cleanup jobs: %v", err)
	}
	if len(jobs) != 2 || jobs[0].BlobID != blobs[1].ID || jobs[1].BlobID != blobs[2].ID {
		t.Fatalf("cleanup jobs = %+v, want duplicate-release and pending-delete", jobs)
	}
	quota := loadStorageBlobRepositoryQuota(t, db)
	if quota.UsedBytes != 60 || quota.ReservedBytes != 0 {
		t.Fatalf("quota before physical cleanup = %+v, want used=60 reserved=0", quota)
	}
}

func loadStorageBlobRepositoryQuota(t *testing.T, db *gorm.DB) model.SystemStorageQuota {
	t.Helper()
	var quota model.SystemStorageQuota
	if err := db.First(&quota, "id = ?", systemStorageQuotaRowID).Error; err != nil {
		t.Fatalf("load storage quota: %v", err)
	}
	return quota
}
