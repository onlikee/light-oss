package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
)

func TestStorageCleanupWorkerRetriesPersistentJobAfterDeleteFailure(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	now := time.Now().UTC().Truncate(time.Millisecond)
	blob := createCleanupTestBlob(t, db, model.StorageBlob{
		ID:          "delete-retry",
		StoragePath: "objects/delete-retry.bin",
		Size:        10,
		Status:      model.StorageBlobStatusPendingDelete,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	setCleanupTestQuota(t, db, 10, 0)
	job := createCleanupTestJob(t, db, blob, now)
	store.put(blob.StoragePath, []byte("0123456789"))
	deleteErr := errors.New("blob store delete failed")
	store.deleteErr = deleteErr
	store.deleteFailures = 1
	worker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Hour)

	err := worker.RunOnce(context.Background(), now)
	if !errors.Is(err, deleteErr) {
		t.Fatalf("first cleanup error = %v, want %v", err, deleteErr)
	}
	if !store.has(blob.StoragePath) {
		t.Fatal("failed delete removed the physical blob")
	}
	var failedJob model.StorageCleanupJob
	if err := db.First(&failedJob, job.ID).Error; err != nil {
		t.Fatalf("load failed cleanup job: %v", err)
	}
	if failedJob.RetryCount != 1 || failedJob.LeaseOwner != nil || failedJob.LeaseExpiresAt != nil {
		t.Fatalf("failed cleanup job = %+v, want retry_count=1 without lease", failedJob)
	}
	if !strings.Contains(failedJob.LastError, deleteErr.Error()) {
		t.Fatalf("last error = %q, want delete failure", failedJob.LastError)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.UsedBytes != 10 {
		t.Fatalf("used bytes after failed cleanup = %d, want 10", quota.UsedBytes)
	}

	if err := worker.RunOnce(context.Background(), failedJob.NextRetryAt.Add(time.Millisecond)); err != nil {
		t.Fatalf("retry cleanup: %v", err)
	}
	assertCleanupTestRemoved(t, db, blob.ID)
	if store.has(blob.StoragePath) {
		t.Fatal("successful retry left physical blob behind")
	}
	quota = loadStorageLifecycleQuota(t, db)
	if quota.UsedBytes != 0 {
		t.Fatalf("used bytes after successful cleanup = %d, want 0", quota.UsedBytes)
	}
}

func TestStorageCleanupWorkerRecoversExpiredStaging(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	now := time.Now().UTC().Truncate(time.Millisecond)
	stagingPath := "staging/expired.tmp"
	blob := createCleanupTestBlob(t, db, model.StorageBlob{
		ID:          "expired-staging",
		StoragePath: "objects/expired.bin",
		StagingPath: &stagingPath,
		Size:        8,
		Status:      model.StorageBlobStatusStaging,
		CreatedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:   now.Add(-2 * time.Hour),
	})
	setCleanupTestQuota(t, db, 0, 8)
	store.put(stagingPath, []byte("partial"))
	store.put(blob.StoragePath, []byte("committed"))
	worker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Hour)

	if err := worker.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("recover expired staging: %v", err)
	}
	assertCleanupTestRemoved(t, db, blob.ID)
	if store.has(stagingPath) || store.has(blob.StoragePath) {
		t.Fatal("expired staging recovery left physical files behind")
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 0 {
		t.Fatalf("quota after expired staging recovery = %+v, want zero usage", quota)
	}
}

func TestStorageCleanupWorkerSealsExpiredStagingBeforePhysicalDelete(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	baseStore := newFakeBlobStore()
	store := &blockingDeleteBlobStore{
		fakeBlobStore: baseStore,
		deleteStarted: make(chan struct{}),
		allowDelete:   make(chan struct{}),
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	stagingPath := "staging/racing.tmp"
	blob := createCleanupTestBlob(t, db, model.StorageBlob{
		ID:          "racing-staging",
		StoragePath: "objects/racing.bin",
		StagingPath: &stagingPath,
		Size:        8,
		Status:      model.StorageBlobStatusStaging,
		CreatedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:   now.Add(-2 * time.Hour),
	})
	setCleanupTestQuota(t, db, 0, 8)
	store.put(stagingPath, []byte("partial"))
	store.put(blob.StoragePath, []byte("committed"))
	worker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Hour)
	deleteAllowed := false
	defer func() {
		if !deleteAllowed {
			close(store.allowDelete)
		}
	}()

	workerResult := make(chan error, 1)
	go func() {
		workerResult <- worker.RunOnce(context.Background(), now)
	}()
	select {
	case <-store.deleteStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup worker did not reach physical delete")
	}

	sealed, err := blobRepo.FindByID(context.Background(), blob.ID)
	if err != nil {
		t.Fatalf("load sealed staging blob: %v", err)
	}
	if sealed.Status != model.StorageBlobStatusPendingDelete || sealed.StagingPath == nil {
		t.Fatalf("sealed staging blob = %+v, want pending_delete with staging path", sealed)
	}
	err = blobRepo.Transaction(context.Background(), func(txRepo *repository.StorageBlobRepository) error {
		return txRepo.ActivateStagingBatch(context.Background(), []repository.StorageBlobActivation{{
			ID:         blob.ID,
			ActualSize: 8,
		}}, 100)
	})
	if err == nil || !strings.Contains(err.Error(), string(model.StorageBlobStatusPendingDelete)) {
		t.Fatalf("activation error = %v, want pending_delete rejection", err)
	}

	close(store.allowDelete)
	deleteAllowed = true
	if err := <-workerResult; err != nil {
		t.Fatalf("complete expired staging cleanup: %v", err)
	}
	assertCleanupTestRemoved(t, db, blob.ID)
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 0 {
		t.Fatalf("quota after sealed staging cleanup = %+v, want zero usage", quota)
	}
}

func TestStorageCleanupWorkerLeaseTakeoverCompletesAfterDatabaseFailure(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	now := time.Now().UTC().Truncate(time.Millisecond)
	blob := createCleanupTestBlob(t, db, model.StorageBlob{
		ID:          "database-retry",
		StoragePath: "objects/database-retry.bin",
		Size:        12,
		Status:      model.StorageBlobStatusPendingDelete,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	setCleanupTestQuota(t, db, 12, 0)
	job := createCleanupTestJob(t, db, blob, now)
	store.put(blob.StoragePath, []byte("physical-data"))
	databaseErr := errors.New("delete blob row failed")
	failDelete := true
	const callbackName = "test:fail_storage_blob_delete_once"
	if err := db.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if failDelete && tx.Statement.Schema != nil && tx.Statement.Schema.Table == "storage_blobs" {
			failDelete = false
			tx.AddError(databaseErr)
		}
	}); err != nil {
		t.Fatalf("register delete callback: %v", err)
	}
	t.Cleanup(func() {
		db.Callback().Delete().Remove(callbackName)
	})

	firstWorker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Hour)
	err := firstWorker.RunOnce(context.Background(), now)
	if !errors.Is(err, databaseErr) {
		t.Fatalf("first cleanup error = %v, want %v", err, databaseErr)
	}
	if store.has(blob.StoragePath) {
		t.Fatal("physical delete should succeed before injected database failure")
	}
	if _, err := blobRepo.FindByID(context.Background(), blob.ID); err != nil {
		t.Fatalf("blob row should remain after rolled back completion: %v", err)
	}
	var leasedJob model.StorageCleanupJob
	if err := db.First(&leasedJob, job.ID).Error; err != nil {
		t.Fatalf("load leased cleanup job: %v", err)
	}
	if leasedJob.LeaseOwner == nil || !strings.HasPrefix(*leasedJob.LeaseOwner, firstWorker.owner+":") || leasedJob.LeaseExpiresAt == nil {
		t.Fatalf("cleanup job lease = %+v, want first worker lease", leasedJob)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.UsedBytes != 12 {
		t.Fatalf("used bytes after rolled back completion = %d, want 12", quota.UsedBytes)
	}

	secondWorker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Hour)
	if err := secondWorker.RunOnce(context.Background(), leasedJob.LeaseExpiresAt.Add(-time.Millisecond)); err != nil {
		t.Fatalf("cleanup before lease expiry: %v", err)
	}
	if _, err := blobRepo.FindByID(context.Background(), blob.ID); err != nil {
		t.Fatalf("blob should remain before lease expiry: %v", err)
	}

	if err := secondWorker.RunOnce(context.Background(), leasedJob.LeaseExpiresAt.Add(time.Millisecond)); err != nil {
		t.Fatalf("cleanup after lease takeover: %v", err)
	}
	assertCleanupTestRemoved(t, db, blob.ID)
	quota = loadStorageLifecycleQuota(t, db)
	if quota.UsedBytes != 0 {
		t.Fatalf("used bytes after lease takeover = %d, want 0", quota.UsedBytes)
	}
}

func TestStorageCleanupWorkerRenewsLeaseDuringSlowDelete(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	baseStore := newFakeBlobStore()
	store := &blockingDeleteBlobStore{
		fakeBlobStore: baseStore,
		deleteStarted: make(chan struct{}),
		allowDelete:   make(chan struct{}),
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	blob := createCleanupTestBlob(t, db, model.StorageBlob{
		ID:          "slow-delete",
		StoragePath: "objects/slow-delete.bin",
		Size:        8,
		Status:      model.StorageBlobStatusPendingDelete,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	setCleanupTestQuota(t, db, 8, 0)
	job := createCleanupTestJob(t, db, blob, now)
	store.put(blob.StoragePath, []byte("12345678"))

	firstWorker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Hour)
	firstWorker.lease = 90 * time.Millisecond
	deleteAllowed := false
	defer func() {
		if !deleteAllowed {
			close(store.allowDelete)
		}
	}()
	result := make(chan error, 1)
	go func() {
		result <- firstWorker.RunOnce(context.Background(), now)
	}()
	select {
	case <-store.deleteStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup worker did not reach physical delete")
	}

	var initiallyLeased model.StorageCleanupJob
	if err := db.First(&initiallyLeased, job.ID).Error; err != nil {
		t.Fatalf("load initial lease: %v", err)
	}
	if initiallyLeased.LeaseOwner == nil || initiallyLeased.LeaseExpiresAt == nil {
		t.Fatalf("initial cleanup lease is missing: %+v", initiallyLeased)
	}

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var renewed model.StorageCleanupJob
		if err := db.First(&renewed, job.ID).Error; err != nil {
			t.Fatalf("load renewed lease: %v", err)
		}
		if renewed.LeaseExpiresAt != nil && renewed.LeaseExpiresAt.After(*initiallyLeased.LeaseExpiresAt) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("cleanup lease was not renewed during slow delete")
		case <-ticker.C:
		}
	}

	secondWorker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Hour)
	secondWorker.lease = firstWorker.lease
	if err := secondWorker.RunOnce(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("run competing cleanup worker: %v", err)
	}
	var stillOwned model.StorageCleanupJob
	if err := db.First(&stillOwned, job.ID).Error; err != nil {
		t.Fatalf("load lease after competing worker: %v", err)
	}
	if stillOwned.LeaseOwner == nil || *stillOwned.LeaseOwner != *initiallyLeased.LeaseOwner {
		t.Fatalf("competing worker took a renewed lease: %+v", stillOwned)
	}

	close(store.allowDelete)
	deleteAllowed = true
	if err := <-result; err != nil {
		t.Fatalf("complete slow cleanup: %v", err)
	}
	assertCleanupTestRemoved(t, db, blob.ID)
}

func createCleanupTestBlob(t *testing.T, db *gorm.DB, blob model.StorageBlob) model.StorageBlob {
	t.Helper()
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create cleanup test blob: %v", err)
	}
	return blob
}

func createCleanupTestJob(
	t *testing.T,
	db *gorm.DB,
	blob model.StorageBlob,
	now time.Time,
) model.StorageCleanupJob {
	t.Helper()
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
	return job
}

func setCleanupTestQuota(t *testing.T, db *gorm.DB, usedBytes uint64, reservedBytes uint64) {
	t.Helper()
	if err := db.Model(&model.SystemStorageQuota{}).
		Where("id = ?", 1).
		Updates(map[string]any{
			"used_bytes":     usedBytes,
			"reserved_bytes": reservedBytes,
		}).Error; err != nil {
		t.Fatalf("set cleanup test quota: %v", err)
	}
}

func assertCleanupTestRemoved(t *testing.T, db *gorm.DB, blobID string) {
	t.Helper()
	var blob model.StorageBlob
	if err := db.First(&blob, "id = ?", blobID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("find cleaned blob error = %v, want record not found", err)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageCleanupJob{}); got != 0 {
		t.Fatalf("cleanup job rows = %d, want 0", got)
	}
}

type blockingDeleteBlobStore struct {
	*fakeBlobStore
	deleteStarted chan struct{}
	allowDelete   chan struct{}
	deleteOnce    sync.Once
}

func (s *blockingDeleteBlobStore) Delete(path string) error {
	s.deleteOnce.Do(func() {
		close(s.deleteStarted)
		<-s.allowDelete
	})
	return s.fakeBlobStore.Delete(path)
}
