package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/storage"
)

func TestStorageReconcilerRegistersOrphanWithoutDeletingPhysicalFile(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	now := time.Now().UTC()
	tracked := model.StorageBlob{
		ID:          "tracked-active",
		StoragePath: "objects/tracked.bin",
		Size:        4,
		RefCount:    1,
		Status:      model.StorageBlobStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&tracked).Error; err != nil {
		t.Fatalf("create tracked blob: %v", err)
	}
	createReconciliationObject(t, db, "tracked", tracked.StoragePath, tracked.Size)
	setCleanupTestQuota(t, db, 4, 0)
	store.put(tracked.StoragePath, []byte("data"))
	orphanPath := "objects/orphan.bin"
	store.put(orphanPath, []byte("orphan"))
	reconciler := NewStorageReconciler(zap.NewNop(), store, blobRepo, time.Hour)

	report, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile storage: %v", err)
	}
	if report.TrackedBlobs != 1 || report.RegisteredOrphans != 1 || report.MissingActive != 0 {
		t.Fatalf("reconciliation report = %+v", report)
	}
	orphan, err := blobRepo.FindByStoragePath(context.Background(), orphanPath)
	if err != nil {
		t.Fatalf("find registered orphan: %v", err)
	}
	if orphan.Status != model.StorageBlobStatusOrphaned || orphan.Size != 6 || orphan.RefCount != 0 {
		t.Fatalf("registered orphan = %+v", orphan)
	}
	if !store.has(orphanPath) {
		t.Fatal("reconciliation deleted the orphan physical file")
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.UsedBytes != 10 || quota.ReconciledAt == nil || quota.StorageID == nil {
		t.Fatalf("quota after reconciliation = %+v, want used=10, storage identity, and completion marker", quota)
	}

	report, err = reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("repeat reconciliation: %v", err)
	}
	if report.RegisteredOrphans != 0 {
		t.Fatalf("repeat reconciliation registered %d orphans, want 0", report.RegisteredOrphans)
	}
	quota = loadStorageLifecycleQuota(t, db)
	if quota.UsedBytes != 10 {
		t.Fatalf("repeat reconciliation changed used bytes to %d", quota.UsedBytes)
	}
}

func TestStorageReconcilerFailsWhenActivePhysicalBlobIsMissing(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	wrongRoot := newFakeBlobStore()
	wrongRoot.identity = "22222222-2222-4222-8222-222222222222"
	now := time.Now().UTC()
	blob := model.StorageBlob{
		ID:          "missing-active",
		StoragePath: "objects/missing.bin",
		Size:        7,
		RefCount:    1,
		Status:      model.StorageBlobStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create active blob: %v", err)
	}
	createReconciliationObject(t, db, "missing", blob.StoragePath, blob.Size)
	setCleanupTestQuota(t, db, 7, 0)
	wrongRootOrphanPath := "objects/wrong-root-orphan.bin"
	wrongRoot.put(wrongRootOrphanPath, []byte("wrong-root"))
	reconciler := NewStorageReconciler(zap.NewNop(), wrongRoot, blobRepo, time.Hour)

	report, err := reconciler.Reconcile(context.Background())
	if err == nil {
		t.Fatal("reconciliation should fail for missing active physical data")
	}
	if !strings.Contains(err.Error(), "active blobs without physical content") || !strings.Contains(err.Error(), blob.StoragePath) {
		t.Fatalf("reconciliation error = %q, want missing active path", err)
	}
	if report == nil || report.MissingActive != 1 {
		t.Fatalf("reconciliation report = %+v, want one missing active blob", report)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReconciledAt != nil {
		t.Fatalf("failed reconciliation set completion marker %v", quota.ReconciledAt)
	}
	if quota.StorageID != nil {
		t.Fatalf("failed first reconciliation retained storage identity %q", *quota.StorageID)
	}
	if quota.UsedBytes != blob.Size {
		t.Fatalf("failed reconciliation changed used bytes to %d, want %d", quota.UsedBytes, blob.Size)
	}
	if _, err := blobRepo.FindByStoragePath(context.Background(), wrongRootOrphanPath); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("failed reconciliation registered wrong-root orphan: %v", err)
	}
	if _, err := blobRepo.FindByID(context.Background(), blob.ID); err != nil {
		t.Fatalf("failed reconciliation changed active blob: %v", err)
	}

	correctRoot := newFakeBlobStore()
	correctRoot.identity = "33333333-3333-4333-8333-333333333333"
	correctRoot.put(blob.StoragePath, []byte("content"))
	correctReconciler := NewStorageReconciler(zap.NewNop(), correctRoot, blobRepo, time.Hour)
	correctReport, err := correctReconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile correct storage root: %v", err)
	}
	if correctReport.RegisteredOrphans != 0 || correctReport.MissingActive != 0 {
		t.Fatalf("correct-root reconciliation report = %+v", correctReport)
	}
	quota = loadStorageLifecycleQuota(t, db)
	if quota.StorageID == nil || *quota.StorageID != correctRoot.identity || quota.UsedBytes != blob.Size {
		t.Fatalf("quota after correct-root reconciliation = %+v", quota)
	}
	if rows := countStorageLifecycleRows(t, db, &model.StorageBlob{}); rows != 1 {
		t.Fatalf("storage blob rows after correct-root reconciliation = %d, want 1", rows)
	}
}

func TestStorageReconcilerRetainsIdentityAfterPartialOrphanRegistration(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	setCleanupTestQuota(t, db, 0, 0)
	store := &managedFilesReconciliationStore{
		identity: "44444444-4444-4444-8444-444444444444",
		files: []storage.ManagedFileInfo{
			{RelativePath: "objects/first-orphan.bin", Size: 3},
			{RelativePath: "objects/invalid-orphan.bin", Size: -1},
		},
	}
	reconciler := NewStorageReconciler(zap.NewNop(), store, blobRepo, time.Hour)

	report, err := reconciler.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "negative size") {
		t.Fatalf("reconciliation error = %v, want negative managed file size", err)
	}
	if report == nil || report.RegisteredOrphans != 1 {
		t.Fatalf("partial reconciliation report = %+v, want one registered orphan", report)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.StorageID == nil || *quota.StorageID != store.identity || quota.UsedBytes != 3 {
		t.Fatalf("quota after partial reconciliation = %+v", quota)
	}
	orphan, err := blobRepo.FindByStoragePath(context.Background(), store.files[0].RelativePath)
	if err != nil {
		t.Fatalf("find first registered orphan: %v", err)
	}
	if orphan.Status != model.StorageBlobStatusOrphaned || orphan.Size != 3 {
		t.Fatalf("first registered orphan = %+v", orphan)
	}
}

func TestStorageReconcilerPropagatesUnexpectedStatFailure(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	now := time.Now().UTC()
	blob := model.StorageBlob{
		ID:          "stat-error",
		StoragePath: "objects/stat-error.bin",
		Size:        1,
		RefCount:    1,
		Status:      model.StorageBlobStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create active blob: %v", err)
	}
	createReconciliationObject(t, db, "stat-error", blob.StoragePath, blob.Size)
	setCleanupTestQuota(t, db, 1, 0)
	staleReconciledAt := now.Add(-time.Hour)
	if err := db.Model(&model.SystemStorageQuota{}).
		Where("id = ?", 1).
		UpdateColumn("reconciled_at", staleReconciledAt).Error; err != nil {
		t.Fatalf("seed stale reconciliation marker: %v", err)
	}
	statErr := errors.New("stat backend unavailable")
	store := &failingReconciliationStore{statErr: statErr}
	reconciler := NewStorageReconciler(zap.NewNop(), store, blobRepo, time.Hour)

	_, err := reconciler.Reconcile(context.Background())
	if !errors.Is(err, statErr) {
		t.Fatalf("reconciliation error = %v, want %v", err, statErr)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReconciledAt != nil {
		t.Fatalf("failed reconciliation retained stale completion marker %v", quota.ReconciledAt)
	}
}

func TestStorageReconcilerRejectsQuotaLedgerMismatch(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	now := time.Now().UTC()
	blob := model.StorageBlob{
		ID:          "quota-mismatch",
		StoragePath: "objects/quota-mismatch.bin",
		Size:        4,
		RefCount:    1,
		Status:      model.StorageBlobStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create active blob: %v", err)
	}
	createReconciliationObject(t, db, "quota-mismatch", blob.StoragePath, blob.Size)
	setCleanupTestQuota(t, db, 3, 0)
	staleReconciledAt := now.Add(-time.Hour)
	if err := db.Model(&model.SystemStorageQuota{}).
		Where("id = ?", 1).
		UpdateColumn("reconciled_at", staleReconciledAt).Error; err != nil {
		t.Fatalf("seed stale reconciliation marker: %v", err)
	}
	store.put(blob.StoragePath, []byte("data"))
	reconciler := NewStorageReconciler(zap.NewNop(), store, blobRepo, time.Hour)

	_, err := reconciler.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "storage quota ledger mismatch") {
		t.Fatalf("reconciliation error = %v, want quota ledger mismatch", err)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReconciledAt != nil {
		t.Fatalf("failed reconciliation set completion marker %v", quota.ReconciledAt)
	}
}

func TestStorageReconcilerRejectsMetadataReferenceWithoutLedger(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	createReconciliationObject(t, db, "missing-ledger", "objects/missing-ledger.bin", 4)
	reconciler := NewStorageReconciler(zap.NewNop(), newFakeBlobStore(), blobRepo, time.Hour)

	_, err := reconciler.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "has no storage blob ledger entry") {
		t.Fatalf("reconciliation error = %v, want missing ledger entry", err)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReconciledAt != nil {
		t.Fatalf("failed reconciliation set completion marker %v", quota.ReconciledAt)
	}
}

func TestStorageReconcilerRejectsReferenceCountMismatch(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	now := time.Now().UTC()
	blob := model.StorageBlob{
		ID:          "reference-mismatch",
		StoragePath: "objects/reference-mismatch.bin",
		Size:        4,
		RefCount:    1,
		Status:      model.StorageBlobStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create active blob: %v", err)
	}
	createReconciliationObject(t, db, "reference-object", blob.StoragePath, blob.Size)
	createReconciliationRecycleObject(t, db, "reference-recycle", blob.StoragePath, blob.Size)
	setCleanupTestQuota(t, db, blob.Size, 0)
	store.put(blob.StoragePath, []byte("data"))
	reconciler := NewStorageReconciler(zap.NewNop(), store, blobRepo, time.Hour)

	_, err := reconciler.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not match metadata references 2") {
		t.Fatalf("reconciliation error = %v, want reference count mismatch", err)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReconciledAt != nil {
		t.Fatalf("failed reconciliation set completion marker %v", quota.ReconciledAt)
	}
}

func TestStorageReconcilerRejectsMetadataSizeMismatch(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	now := time.Now().UTC()
	blob := model.StorageBlob{
		ID:          "metadata-size-mismatch",
		StoragePath: "objects/metadata-size-mismatch.bin",
		Size:        4,
		RefCount:    1,
		Status:      model.StorageBlobStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create active blob: %v", err)
	}
	createReconciliationObject(t, db, "metadata-size-mismatch", blob.StoragePath, 3)
	setCleanupTestQuota(t, db, blob.Size, 0)
	store.put(blob.StoragePath, []byte("data"))
	reconciler := NewStorageReconciler(zap.NewNop(), store, blobRepo, time.Hour)

	_, err := reconciler.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not match metadata size range 3..3") {
		t.Fatalf("reconciliation error = %v, want metadata size mismatch", err)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReconciledAt != nil {
		t.Fatalf("failed reconciliation set completion marker %v", quota.ReconciledAt)
	}
}

func TestStorageReconcilerRejectsPendingDeleteWithoutCleanupJob(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	now := time.Now().UTC()
	blob := model.StorageBlob{
		ID:          "untracked-pending-delete",
		StoragePath: "objects/untracked-pending-delete.bin",
		Size:        4,
		Status:      model.StorageBlobStatusPendingDelete,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create pending-delete blob: %v", err)
	}
	setCleanupTestQuota(t, db, blob.Size, 0)
	reconciler := NewStorageReconciler(zap.NewNop(), newFakeBlobStore(), blobRepo, time.Hour)

	_, err := reconciler.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "has no cleanup job") {
		t.Fatalf("reconciliation error = %v, want missing cleanup job", err)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReconciledAt != nil {
		t.Fatalf("failed reconciliation set completion marker %v", quota.ReconciledAt)
	}
}

func TestStorageReconcilerSealsEveryExpiredStagingBeforeMarkingReady(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 1000)
	store := newFakeBlobStore()
	now := time.Now().UTC()
	count := defaultCleanupBatchSize + 1
	blobs := make([]model.StorageBlob, 0, count)
	for index := 0; index < count; index++ {
		stagingPath := fmt.Sprintf("staging/expired-%d.part", index)
		blobs = append(blobs, model.StorageBlob{
			ID:          fmt.Sprintf("expired-%d", index),
			StoragePath: fmt.Sprintf("objects/expired-%d.bin", index),
			StagingPath: &stagingPath,
			Size:        1,
			Status:      model.StorageBlobStatusStaging,
			CreatedAt:   now.Add(-2 * time.Hour),
			UpdatedAt:   now.Add(-2 * time.Hour),
		})
		store.put(stagingPath, []byte("x"))
	}
	if err := db.Create(&blobs).Error; err != nil {
		t.Fatalf("create expired staging blobs: %v", err)
	}
	setCleanupTestQuota(t, db, 0, uint64(count))
	reconciler := NewStorageReconciler(zap.NewNop(), store, blobRepo, time.Hour)

	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile storage: %v", err)
	}
	var pendingCount int64
	if err := db.Model(&model.StorageBlob{}).
		Where("status = ? AND staging_path IS NOT NULL", model.StorageBlobStatusPendingDelete).
		Count(&pendingCount).Error; err != nil {
		t.Fatalf("count sealed staging blobs: %v", err)
	}
	if pendingCount != int64(count) {
		t.Fatalf("sealed staging blobs = %d, want %d", pendingCount, count)
	}
	if jobs := countStorageLifecycleRows(t, db, &model.StorageCleanupJob{}); jobs != int64(count) {
		t.Fatalf("cleanup jobs = %d, want %d", jobs, count)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != uint64(count) || quota.UsedBytes != 0 || quota.ReconciledAt == nil {
		t.Fatalf("quota after sealing expired staging = %+v", quota)
	}
}

func TestStorageReconcilerRejectsActivePhysicalSizeMismatch(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	now := time.Now().UTC()
	blob := model.StorageBlob{
		ID:          "physical-size-mismatch",
		StoragePath: "objects/physical-size-mismatch.bin",
		Size:        5,
		RefCount:    1,
		Status:      model.StorageBlobStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&blob).Error; err != nil {
		t.Fatalf("create active blob: %v", err)
	}
	createReconciliationObject(t, db, "physical-size-mismatch", blob.StoragePath, blob.Size)
	setCleanupTestQuota(t, db, blob.Size, 0)
	store.put(blob.StoragePath, []byte("data"))
	reconciler := NewStorageReconciler(zap.NewNop(), store, blobRepo, time.Hour)

	_, err := reconciler.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "physical size 4 does not match ledger size 5") {
		t.Fatalf("reconciliation error = %v, want physical size mismatch", err)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReconciledAt != nil {
		t.Fatalf("failed reconciliation set completion marker %v", quota.ReconciledAt)
	}
}

func createReconciliationObject(t *testing.T, db *gorm.DB, objectKey string, storagePath string, size uint64) {
	t.Helper()
	now := time.Now().UTC()
	ensureReconciliationBucket(t, db, now)
	object := model.Object{
		BucketName:       "reconciliation",
		ObjectKey:        objectKey,
		OriginalFilename: objectKey,
		StoragePath:      storagePath,
		Size:             int64(size),
		ContentType:      "application/octet-stream",
		ETag:             "etag",
		Visibility:       model.VisibilityPrivate,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := db.Create(&object).Error; err != nil {
		t.Fatalf("create object reference: %v", err)
	}
}

func createReconciliationRecycleObject(t *testing.T, db *gorm.DB, objectKey string, storagePath string, size uint64) {
	t.Helper()
	now := time.Now().UTC()
	ensureReconciliationBucket(t, db, now)
	item := model.RecycleBinObject{
		BucketName:       "reconciliation",
		ObjectKey:        objectKey,
		OriginalFilename: objectKey,
		StoragePath:      storagePath,
		Size:             int64(size),
		ContentType:      "application/octet-stream",
		ETag:             "etag",
		Visibility:       model.VisibilityPrivate,
		CreatedAt:        now,
		DeletedAt:        now,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create recycle-bin reference: %v", err)
	}
}

func ensureReconciliationBucket(t *testing.T, db *gorm.DB, now time.Time) {
	t.Helper()
	bucket := model.Bucket{Name: "reconciliation", CreatedAt: now, UpdatedAt: now}
	if err := db.Where("name = ?", bucket.Name).FirstOrCreate(&bucket).Error; err != nil {
		t.Fatalf("create reconciliation bucket: %v", err)
	}
}

type failingReconciliationStore struct {
	statErr error
}

func (s *failingReconciliationStore) Stat(string) (os.FileInfo, error) {
	return nil, s.statErr
}

func (s *failingReconciliationStore) Identity(context.Context) (string, error) {
	return "11111111-1111-4111-8111-111111111111", nil
}

func (s *failingReconciliationStore) WalkManaged(context.Context) ([]storage.ManagedFileInfo, error) {
	return nil, nil
}

type managedFilesReconciliationStore struct {
	identity string
	files    []storage.ManagedFileInfo
}

func (s *managedFilesReconciliationStore) Identity(context.Context) (string, error) {
	return s.identity, nil
}

func (s *managedFilesReconciliationStore) Stat(string) (os.FileInfo, error) {
	return nil, os.ErrNotExist
}

func (s *managedFilesReconciliationStore) WalkManaged(context.Context) ([]storage.ManagedFileInfo, error) {
	return s.files, nil
}
