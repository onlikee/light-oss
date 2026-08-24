package service

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"light-oss/backend/internal/model"
)

func (s *BlobLifecycleService) triggerCleanup(ctx context.Context) {
	if s.runCleanup != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if err := s.runCleanup(cleanupCtx); err != nil {
			s.logger.Warn("immediate storage cleanup failed", zap.Error(err))
		}
		cancel()
	}
	if s.wakeCleanup != nil {
		s.wakeCleanup()
	}
}

func (s *BlobLifecycleService) Abort(ctx context.Context, stagedBlobs ...*StagedBlob) {
	stopStagedBlobLeases(stagedBlobs)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	s.abortBatch(cleanupCtx, stagedBlobs)
}

func (s *BlobLifecycleService) startStagingLeaseHeartbeat(
	ctx context.Context,
	blobIDs []string,
) *stagingLeaseHeartbeat {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	heartbeat := &stagingLeaseHeartbeat{
		ctx:    heartbeatCtx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	interval := s.stagingLease / 3
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	go func() {
		defer close(heartbeat.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := s.blobRepo.RenewStagingLease(
					heartbeatCtx,
					blobIDs,
					s.stagingLease,
					blobLedgerWriteBatchSize,
				); err != nil {
					heartbeat.fail(err)
					return
				}
			}
		}
	}()
	return heartbeat
}

func stopStagedBlobLeases(stagedBlobs []*StagedBlob) {
	seen := make(map[*stagingLeaseHeartbeat]struct{})
	for _, staged := range stagedBlobs {
		if staged == nil || staged.lease == nil {
			continue
		}
		if _, exists := seen[staged.lease]; exists {
			continue
		}
		seen[staged.lease] = struct{}{}
		staged.lease.stop()
	}
}

func stagedBlobsLeaseError(stagedBlobs []*StagedBlob) error {
	for _, staged := range stagedBlobs {
		if staged == nil || staged.lease == nil {
			continue
		}
		if err := staged.lease.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *BlobLifecycleService) abortStaging(ctx context.Context, blob *model.StorageBlob) {
	stagingPath := ""
	if blob.StagingPath != nil {
		stagingPath = *blob.StagingPath
	}
	s.abortBatch(ctx, []*StagedBlob{{
		ID:          blob.ID,
		StoragePath: blob.StoragePath,
		StagingPath: stagingPath,
	}})
}

func (s *BlobLifecycleService) abortBatch(ctx context.Context, stagedBlobs []*StagedBlob) {
	readyToRelease := make([]*StagedBlob, 0, len(stagedBlobs))
	cleanupErrors := make(map[string]error)
	cleanupBlobs := make(map[string]*StagedBlob)
	for _, staged := range stagedBlobs {
		if staged == nil || staged.ID == "" {
			continue
		}
		var deleteErr error
		if staged.StagingPath != "" {
			deleteErr = errors.Join(deleteErr, s.store.Delete(staged.StagingPath))
		}
		if staged.StoragePath != "" {
			deleteErr = errors.Join(deleteErr, s.store.Delete(staged.StoragePath))
		}
		if deleteErr == nil {
			readyToRelease = append(readyToRelease, staged)
			continue
		}
		cleanupBlobs[staged.ID] = staged
		cleanupErrors[staged.ID] = deleteErr
	}

	if len(readyToRelease) > 0 {
		ids := make([]string, 0, len(readyToRelease))
		for _, staged := range readyToRelease {
			ids = append(ids, staged.ID)
		}
		if err := s.blobRepo.ReleaseStagingBatch(ctx, ids, blobLedgerWriteBatchSize); err != nil {
			for _, staged := range readyToRelease {
				cleanupBlobs[staged.ID] = staged
				cleanupErrors[staged.ID] = err
			}
		}
	}

	if len(cleanupBlobs) == 0 {
		return
	}
	now := time.Now().UTC()
	jobs := make([]model.StorageCleanupJob, 0, len(cleanupBlobs))
	for blobID, staged := range cleanupBlobs {
		jobs = append(jobs, model.StorageCleanupJob{
			BlobID:      blobID,
			StoragePath: staged.StoragePath,
			NextRetryAt: now,
			LastError:   cleanupErrors[blobID].Error(),
		})
	}
	if err := s.blobRepo.EnqueueCleanupBatch(ctx, jobs, blobLedgerWriteBatchSize); err != nil {
		s.logger.Error(
			"track staging cleanup failed",
			zap.Int("blob_count", len(jobs)),
			zap.Error(err),
		)
		return
	}
	if s.wakeCleanup != nil {
		s.wakeCleanup()
	}
}
