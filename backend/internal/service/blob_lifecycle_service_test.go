package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"light-oss/backend/internal/model"
)

func TestBlobLifecycleStageAndPublishAccountsForActualSize(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)

	staged, err := lifecycle.Stage(context.Background(), strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("stage blob: %v", err)
	}
	if staged.Size != 5 {
		t.Fatalf("staged size = %d, want 5", staged.Size)
	}
	if store.has(staged.StagingPath) {
		t.Fatal("staging file should have been promoted")
	}
	if !store.has(staged.StoragePath) {
		t.Fatal("committed file is missing")
	}

	blob, err := blobRepo.FindByID(context.Background(), staged.ID)
	if err != nil {
		t.Fatalf("find staged blob: %v", err)
	}
	if blob.Status != model.StorageBlobStatusStaging || blob.Size != 8 {
		t.Fatalf("staged blob = %+v, want status=staging size=8", blob)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 8 || quota.UsedBytes != 0 {
		t.Fatalf("quota after stage = %+v, want reserved=8 used=0", quota)
	}

	if err := lifecycle.Publish(context.Background(), []*StagedBlob{staged}, nil); err != nil {
		t.Fatalf("publish blob: %v", err)
	}
	blob, err = blobRepo.FindByID(context.Background(), staged.ID)
	if err != nil {
		t.Fatalf("find active blob: %v", err)
	}
	if blob.Status != model.StorageBlobStatusActive || blob.Size != 5 || blob.RefCount != 1 || blob.StagingPath != nil {
		t.Fatalf("active blob = %+v, want active size=5 ref_count=1 without staging path", blob)
	}
	quota = loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 5 {
		t.Fatalf("quota after publish = %+v, want reserved=0 used=5", quota)
	}
}

func TestBlobLifecycleHeartbeatProtectsSlowActiveStagingFromCleanup(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)
	const stagingLease = 500 * time.Millisecond
	lifecycle.SetStagingLease(stagingLease)
	worker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, stagingLease)

	reader := &blockingStagingReader{
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
	type stageResult struct {
		blob *StagedBlob
		err  error
	}
	result := make(chan stageResult, 1)
	go func() {
		blob, err := lifecycle.Stage(context.Background(), reader)
		result <- stageResult{blob: blob, err: err}
	}()

	select {
	case <-reader.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("staging reader did not block")
	}
	time.Sleep(2 * stagingLease)
	if err := worker.RunOnceAtDatabaseTime(context.Background()); err != nil {
		t.Fatalf("cleanup while staging is active: %v", err)
	}

	var staging model.StorageBlob
	if err := db.Where("status = ?", model.StorageBlobStatusStaging).First(&staging).Error; err != nil {
		t.Fatalf("load active staging blob: %v", err)
	}
	if staging.StagingLeaseExpiresAt == nil {
		t.Fatal("active staging blob lost its lease")
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageCleanupJob{}); got != 0 {
		t.Fatalf("cleanup jobs while staging is active = %d, want 0", got)
	}

	close(reader.release)
	var staged *StagedBlob
	select {
	case stage := <-result:
		if stage.err != nil {
			t.Fatalf("finish slow staging: %v", stage.err)
		}
		staged = stage.blob
	case <-time.After(2 * time.Second):
		t.Fatal("slow staging did not finish")
	}
	if err := lifecycle.Publish(context.Background(), []*StagedBlob{staged}, nil); err != nil {
		t.Fatalf("publish slow staging: %v", err)
	}
	active, err := blobRepo.FindByID(context.Background(), staged.ID)
	if err != nil {
		t.Fatalf("load published blob: %v", err)
	}
	if active.Status != model.StorageBlobStatusActive || active.StagingLeaseExpiresAt != nil {
		t.Fatalf("published blob = %+v, want active without staging lease", active)
	}
}

type blockingStagingReader struct {
	delivered bool
	blocked   chan struct{}
	release   chan struct{}
}

func (r *blockingStagingReader) Read(buffer []byte) (int, error) {
	if !r.delivered {
		r.delivered = true
		return copy(buffer, "slow-upload"), nil
	}
	close(r.blocked)
	<-r.release
	return 0, io.EOF
}

func TestBlobLifecycleStageFailureReleasesReservationAndFiles(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	stageErr := errors.New("stage write failed")
	store.stageErr = stageErr
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)

	_, err := lifecycle.Stage(context.Background(), strings.NewReader("hello"))
	if !errors.Is(err, stageErr) {
		t.Fatalf("stage error = %v, want %v", err, stageErr)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageBlob{}); got != 0 {
		t.Fatalf("storage blob rows = %d, want 0", got)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageCleanupJob{}); got != 0 {
		t.Fatalf("cleanup job rows = %d, want 0", got)
	}
	if store.fileCount() != 0 {
		t.Fatalf("stored files = %d, want 0", store.fileCount())
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 0 {
		t.Fatalf("quota after failed stage = %+v, want zero usage", quota)
	}
}

func TestBlobLifecycleCommitFailureReleasesReservationAndFiles(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	commitErr := errors.New("commit failed")
	store.commitErr = commitErr
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)

	_, err := lifecycle.Stage(context.Background(), strings.NewReader("hello"))
	if !errors.Is(err, commitErr) {
		t.Fatalf("stage error = %v, want %v", err, commitErr)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageBlob{}); got != 0 {
		t.Fatalf("storage blob rows = %d, want 0", got)
	}
	if store.fileCount() != 0 {
		t.Fatalf("stored files = %d, want 0", store.fileCount())
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 0 {
		t.Fatalf("quota after failed commit = %+v, want zero usage", quota)
	}
}

func TestBlobLifecyclePublishDatabaseFailureRollsBackAndAborts(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)
	worker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Hour)
	lifecycle.SetCleanupRunOnce(func(ctx context.Context) error {
		return worker.RunOnce(ctx, time.Now().UTC())
	})
	now := time.Now().UTC()
	if err := db.Create(&model.Bucket{Name: "duplicate", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed bucket: %v", err)
	}

	staged, err := lifecycle.Stage(context.Background(), strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("stage blob: %v", err)
	}
	err = lifecycle.Publish(context.Background(), []*StagedBlob{staged}, func(tx *gorm.DB) ([]string, error) {
		return nil, tx.Create(&model.Bucket{Name: "duplicate", CreatedAt: now, UpdatedAt: now}).Error
	})
	if err == nil {
		t.Fatal("publish should fail on duplicate bucket")
	}

	if got := countStorageLifecycleRows(t, db, &model.StorageBlob{}); got != 0 {
		t.Fatalf("storage blob rows = %d, want 0", got)
	}
	if store.has(staged.StoragePath) || store.has(staged.StagingPath) {
		t.Fatal("failed publish left physical blob data behind")
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 0 {
		t.Fatalf("quota after failed publish = %+v, want zero usage", quota)
	}
	if got := countStorageLifecycleRows(t, db, &model.Bucket{}); got != 1 {
		t.Fatalf("bucket rows = %d, want original row only", got)
	}
}

func TestBlobLifecycleConfirmPublishOutcome(t *testing.T) {
	t.Run("rolled back", func(t *testing.T) {
		db, blobRepo := newStorageLifecycleTestDB(t, 100)
		store := newFakeBlobStore()
		lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)
		staged, err := lifecycle.Stage(context.Background(), strings.NewReader("hello"))
		if err != nil {
			t.Fatalf("stage blob: %v", err)
		}

		outcome, err := lifecycle.confirmPublishOutcome(context.Background(), []*StagedBlob{staged})
		if err != nil {
			t.Fatalf("confirm rolled-back outcome: %v", err)
		}
		if outcome != publishOutcomeRolledBack {
			t.Fatalf("outcome = %d, want rolled back", outcome)
		}
		blob, err := blobRepo.FindByID(context.Background(), staged.ID)
		if err != nil {
			t.Fatalf("find sealed blob: %v", err)
		}
		if blob.Status != model.StorageBlobStatusPendingDelete {
			t.Fatalf("blob status = %s, want pending_delete", blob.Status)
		}
		if got := countStorageLifecycleRows(t, db, &model.StorageCleanupJob{}); got != 1 {
			t.Fatalf("cleanup jobs = %d, want 1", got)
		}
	})

	t.Run("committed", func(t *testing.T) {
		db, blobRepo := newStorageLifecycleTestDB(t, 100)
		store := newFakeBlobStore()
		lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)
		staged, err := lifecycle.Stage(context.Background(), strings.NewReader("hello"))
		if err != nil {
			t.Fatalf("stage blob: %v", err)
		}
		if err := blobRepo.ActivateStaging(context.Background(), staged.ID, staged.Size); err != nil {
			t.Fatalf("activate blob: %v", err)
		}

		outcome, err := lifecycle.confirmPublishOutcome(context.Background(), []*StagedBlob{staged})
		if err != nil {
			t.Fatalf("confirm committed outcome: %v", err)
		}
		if outcome != publishOutcomeCommitted {
			t.Fatalf("outcome = %d, want committed", outcome)
		}
	})

	t.Run("mixed", func(t *testing.T) {
		db, blobRepo := newStorageLifecycleTestDB(t, 100)
		store := newFakeBlobStore()
		lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)
		first, err := lifecycle.Stage(context.Background(), strings.NewReader("first"))
		if err != nil {
			t.Fatalf("stage first blob: %v", err)
		}
		second, err := lifecycle.Stage(context.Background(), strings.NewReader("second"))
		if err != nil {
			t.Fatalf("stage second blob: %v", err)
		}
		if err := blobRepo.ActivateStaging(context.Background(), first.ID, first.Size); err != nil {
			t.Fatalf("activate first blob: %v", err)
		}

		outcome, err := lifecycle.confirmPublishOutcome(context.Background(), []*StagedBlob{first, second})
		if err == nil {
			t.Fatal("mixed state confirmation should fail")
		}
		if outcome != publishOutcomeUnknown {
			t.Fatalf("outcome = %d, want unknown", outcome)
		}
	})
}

func TestBlobLifecyclePublishUncertainOutcomePreservesPhysicalBlob(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)
	staged, err := lifecycle.Stage(context.Background(), strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("stage blob: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql db: %v", err)
	}

	err = lifecycle.Publish(context.Background(), []*StagedBlob{staged}, nil)
	if err == nil {
		t.Fatal("publish should fail when its outcome cannot be confirmed")
	}
	if !store.has(staged.StoragePath) {
		t.Fatal("uncertain publish outcome must preserve the physical blob")
	}
}

func TestBlobLifecycleRepeatedPublishDoesNotReleaseActiveBlob(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)
	staged, err := lifecycle.Stage(context.Background(), strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("stage blob: %v", err)
	}
	if err := lifecycle.Publish(context.Background(), []*StagedBlob{staged}, nil); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	metadataCalled := false
	err = lifecycle.Publish(context.Background(), []*StagedBlob{staged}, func(*gorm.DB) ([]string, error) {
		metadataCalled = true
		return []string{staged.StoragePath}, nil
	})
	if err == nil {
		t.Fatal("repeated publish should be rejected")
	}
	if metadataCalled {
		t.Fatal("repeated publish must fail before running metadata changes")
	}
	if !store.has(staged.StoragePath) {
		t.Fatal("repeated publish removed the active physical blob")
	}
	blob, findErr := blobRepo.FindByID(context.Background(), staged.ID)
	if findErr != nil {
		t.Fatalf("find active blob: %v", findErr)
	}
	if blob.Status != model.StorageBlobStatusActive || blob.RefCount != 1 {
		t.Fatalf("active blob changed after repeated publish: %+v", blob)
	}
}

func TestBlobLifecycleDeleteFailureQueuesDurableCleanup(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	stageErr := errors.New("stage write failed")
	deleteErr := errors.New("delete failed")
	store.stageErr = stageErr
	store.deleteErr = deleteErr
	store.deleteFailures = 1
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)
	wakeCount := 0
	lifecycle.SetCleanupWake(func() { wakeCount++ })

	_, err := lifecycle.Stage(context.Background(), strings.NewReader("hello"))
	if !errors.Is(err, stageErr) {
		t.Fatalf("stage error = %v, want %v", err, stageErr)
	}
	if wakeCount != 1 {
		t.Fatalf("cleanup wake count = %d, want 1", wakeCount)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageBlob{}); got != 1 {
		t.Fatalf("storage blob rows = %d, want 1", got)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageCleanupJob{}); got != 1 {
		t.Fatalf("cleanup job rows = %d, want 1", got)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 8 {
		t.Fatalf("reserved bytes = %d, want 8 until cleanup succeeds", quota.ReservedBytes)
	}

	worker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Hour)
	if err := worker.RunOnce(context.Background(), time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("run cleanup: %v", err)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageBlob{}); got != 0 {
		t.Fatalf("storage blob rows after cleanup = %d, want 0", got)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageCleanupJob{}); got != 0 {
		t.Fatalf("cleanup job rows after cleanup = %d, want 0", got)
	}
	if store.fileCount() != 0 {
		t.Fatalf("stored files after cleanup = %d, want 0", store.fileCount())
	}
	quota = loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 0 {
		t.Fatalf("quota after cleanup = %+v, want zero usage", quota)
	}
}
