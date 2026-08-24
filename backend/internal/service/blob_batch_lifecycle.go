package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/storage"
)

func (s *BlobLifecycleService) StageKnownBatch(ctx context.Context, inputs []BlobBatchInput) ([]*StagedBlob, error) {
	if len(inputs) == 0 {
		return []*StagedBlob{}, nil
	}

	blobs := make([]model.StorageBlob, 0, len(inputs))
	stagedBlobs := make([]*StagedBlob, 0, len(inputs))
	for _, input := range inputs {
		blobID := uuid.NewString()
		stagingPath := storage.NewManagedPath("staging", ".tmp")
		storagePath := storage.NewManagedPath("objects", ".bin")
		reservedSize := input.Size
		if reservedSize == 0 {
			reservedSize = 1
		}
		blobs = append(blobs, model.StorageBlob{
			ID:          blobID,
			StoragePath: storagePath,
			StagingPath: &stagingPath,
			Size:        reservedSize,
			Status:      model.StorageBlobStatusStaging,
		})
		stagedBlobs = append(stagedBlobs, &StagedBlob{
			ID:          blobID,
			StoragePath: storagePath,
			StagingPath: stagingPath,
		})
	}

	if err := s.blobRepo.CreateStagingBatchWithLease(ctx, blobs, blobLedgerWriteBatchSize, s.stagingLease); err != nil {
		if errors.Is(err, repository.ErrStorageQuotaExceeded) {
			s.metrics.RecordReservationFailure()
			return nil, apperrors.New(http.StatusInsufficientStorage, "storage_limit_exceeded", "storage usage exceeds configured limit")
		}
		return nil, err
	}
	blobIDs := make([]string, 0, len(stagedBlobs))
	for _, staged := range stagedBlobs {
		blobIDs = append(blobIDs, staged.ID)
	}
	lease := s.startStagingLeaseHeartbeat(ctx, blobIDs)
	for _, staged := range stagedBlobs {
		staged.lease = lease
	}

	batchCtx, cancel := context.WithCancel(lease.ctx)
	defer cancel()

	indices := make(chan int, len(inputs))
	for index := range inputs {
		indices <- index
	}
	close(indices)

	var firstErr error
	var firstErrOnce sync.Once
	recordError := func(err error) {
		firstErrOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}

	workerCount := min(batchStagingConcurrency, len(inputs))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range indices {
				if batchCtx.Err() != nil {
					return
				}

				input := inputs[index]
				if input.Open == nil {
					recordError(&batchBlobReaderError{operation: "open", err: fmt.Errorf("batch blob reader is not configured")})
					return
				}
				reader, err := input.Open()
				if err != nil {
					recordError(&batchBlobReaderError{operation: "open", err: err})
					return
				}

				staged, stageErr := s.stageKnown(batchCtx, stagedBlobs[index], input.Size, reader)
				closeErr := reader.Close()
				if stageErr != nil {
					recordError(stageErr)
					return
				}
				if closeErr != nil {
					recordError(&batchBlobReaderError{operation: "close", err: closeErr})
					return
				}
				stagedBlobs[index] = staged
			}
		}()
	}
	workers.Wait()

	if firstErr != nil {
		s.Abort(ctx, stagedBlobs...)
		if leaseErr := lease.Err(); leaseErr != nil {
			return nil, errors.Join(firstErr, fmt.Errorf("renew staging lease: %w", leaseErr))
		}
		return nil, firstErr
	}
	if leaseErr := lease.Err(); leaseErr != nil {
		s.Abort(ctx, stagedBlobs...)
		return nil, fmt.Errorf("renew staging lease: %w", leaseErr)
	}

	return stagedBlobs, nil
}

func (s *BlobLifecycleService) stageKnown(
	ctx context.Context,
	prepared *StagedBlob,
	expectedSize uint64,
	reader io.Reader,
) (result *StagedBlob, resultErr error) {
	startedAt := time.Now()
	defer func() {
		var size uint64
		if result != nil {
			size = result.Size
		}
		s.metrics.RecordUploadStaging(size, time.Since(startedAt), resultErr)
	}()

	stagedFile, err := s.store.Stage(ctx, prepared.StagingPath, reader, func(totalBytes int64) error {
		if totalBytes < 0 || uint64(totalBytes) > expectedSize {
			return fmt.Errorf("staged blob exceeds declared size %d", expectedSize)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if stagedFile.Size < 0 || uint64(stagedFile.Size) != expectedSize {
		return nil, fmt.Errorf("staged blob size %d does not match declared size %d", stagedFile.Size, expectedSize)
	}
	if err := s.store.Commit(prepared.StagingPath, prepared.StoragePath); err != nil {
		return nil, err
	}

	result = &StagedBlob{
		ID:          prepared.ID,
		StoragePath: prepared.StoragePath,
		StagingPath: prepared.StagingPath,
		Size:        expectedSize,
		ETag:        stagedFile.ETag,
		lease:       prepared.lease,
	}
	s.logger.Info(
		"object content staged",
		zap.String("blob_id", result.ID),
		zap.String("storage_path", result.StoragePath),
		zap.Uint64("size_bytes", result.Size),
		zap.Duration("duration", time.Since(startedAt)),
	)
	return result, nil
}
