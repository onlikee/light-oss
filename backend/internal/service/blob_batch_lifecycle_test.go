package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"light-oss/backend/internal/model"
)

func TestBlobLifecycleStageKnownBatchReservesAndPublishesAsOneUnit(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)

	staged, err := lifecycle.StageKnownBatch(context.Background(), []BlobBatchInput{
		{
			Size: 5,
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("hello")), nil
			},
		},
		{
			Size: 0,
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("")), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("stage known batch: %v", err)
	}
	if len(staged) != 2 || staged[0].Size != 5 || staged[1].Size != 0 {
		t.Fatalf("staged batch = %+v, want sizes 5 and 0", staged)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 6 || quota.UsedBytes != 0 {
		t.Fatalf("quota after stage = %+v, want reserved=6 used=0", quota)
	}

	if err := lifecycle.Publish(context.Background(), staged, nil); err != nil {
		t.Fatalf("publish known batch: %v", err)
	}
	quota = loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 5 {
		t.Fatalf("quota after publish = %+v, want reserved=0 used=5", quota)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageBlob{}); got != 2 {
		t.Fatalf("active storage blob rows = %d, want 2", got)
	}
}

func TestBlobLifecycleStageKnownBatchSizeMismatchCompensatesEveryBlob(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)

	_, err := lifecycle.StageKnownBatch(context.Background(), []BlobBatchInput{
		{
			Size: 5,
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("hello")), nil
			},
		},
		{
			Size: 4,
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("world")), nil
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "declared size") {
		t.Fatalf("stage known batch error = %v, want declared size mismatch", err)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageBlob{}); got != 0 {
		t.Fatalf("storage blob rows after compensation = %d, want 0", got)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageCleanupJob{}); got != 0 {
		t.Fatalf("cleanup job rows after compensation = %d, want 0", got)
	}
	if store.fileCount() != 0 {
		t.Fatalf("stored files after compensation = %d, want 0", store.fileCount())
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 0 {
		t.Fatalf("quota after compensation = %+v, want zero usage", quota)
	}
}

func TestBlobLifecycleStageKnownBatchOpenFailureCompensatesPreparedRows(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)
	openErr := errors.New("open failed")

	_, err := lifecycle.StageKnownBatch(context.Background(), []BlobBatchInput{
		{
			Size: 5,
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("hello")), nil
			},
		},
		{
			Size: 5,
			Open: func() (io.ReadCloser, error) {
				return nil, openErr
			},
		},
	})
	if !errors.Is(err, openErr) {
		t.Fatalf("stage known batch error = %v, want %v", err, openErr)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageBlob{}); got != 0 {
		t.Fatalf("storage blob rows after open failure = %d, want 0", got)
	}
	if store.fileCount() != 0 {
		t.Fatalf("stored files after open failure = %d, want 0", store.fileCount())
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 0 {
		t.Fatalf("quota after open failure = %+v, want zero usage", quota)
	}
}

func TestBlobLifecycleStageKnownBatchCleanupFailureKeepsOnlyFailedReservation(t *testing.T) {
	db, blobRepo := newStorageLifecycleTestDB(t, 100)
	store := newFakeBlobStore()
	stageErr := errors.New("stage failed")
	deleteErr := errors.New("delete failed")
	store.stageErr = stageErr
	store.deleteErr = deleteErr
	store.deleteFailures = 1
	lifecycle := NewBlobLifecycleService(zap.NewNop(), db, store, blobRepo, 4)

	_, err := lifecycle.StageKnownBatch(context.Background(), []BlobBatchInput{
		{
			Size: 5,
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("hello")), nil
			},
		},
		{
			Size: 5,
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("world")), nil
			},
		},
	})
	if !errors.Is(err, stageErr) {
		t.Fatalf("stage known batch error = %v, want %v", err, stageErr)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageBlob{}); got != 1 {
		t.Fatalf("storage blob rows after partial cleanup = %d, want 1", got)
	}
	if got := countStorageLifecycleRows(t, db, &model.StorageCleanupJob{}); got != 1 {
		t.Fatalf("cleanup jobs after partial cleanup = %d, want 1", got)
	}
	quota := loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 5 || quota.UsedBytes != 0 {
		t.Fatalf("quota after partial cleanup = %+v, want reserved=5 used=0", quota)
	}

	worker := NewStorageCleanupWorker(zap.NewNop(), store, blobRepo, time.Hour)
	if err := worker.RunOnce(context.Background(), time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("run durable cleanup: %v", err)
	}
	quota = loadStorageLifecycleQuota(t, db)
	if quota.ReservedBytes != 0 || quota.UsedBytes != 0 {
		t.Fatalf("quota after durable cleanup = %+v, want zero usage", quota)
	}
}
