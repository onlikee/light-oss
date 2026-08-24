package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
)

func (s *BlobLifecycleService) Publish(
	ctx context.Context,
	stagedBlobs []*StagedBlob,
	publishMetadata func(*gorm.DB) ([]string, error),
) error {
	if leaseErr := stagedBlobsLeaseError(stagedBlobs); leaseErr != nil {
		stopStagedBlobLeases(stagedBlobs)
		s.triggerCleanup(ctx)
		return fmt.Errorf("renew staging lease: %w", leaseErr)
	}
	defer stopStagedBlobLeases(stagedBlobs)

	cleanupQueued := false
	transactionStartedAt := time.Now()
	err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		blobRepo := s.blobRepo.WithDB(tx)
		activations := make([]repository.StorageBlobActivation, 0, len(stagedBlobs))
		for _, staged := range stagedBlobs {
			activations = append(activations, repository.StorageBlobActivation{
				ID:         staged.ID,
				ActualSize: staged.Size,
			})
		}
		if err := blobRepo.ActivateStagingBatch(ctx, activations, blobLedgerWriteBatchSize); err != nil {
			return err
		}

		var releasedPaths []string
		if publishMetadata != nil {
			var err error
			releasedPaths, err = publishMetadata(tx)
			if err != nil {
				return err
			}
		}

		if len(releasedPaths) > 0 {
			if err := blobRepo.ReleaseReferences(ctx, releasedPaths, time.Now().UTC(), blobLedgerWriteBatchSize); err != nil {
				return err
			}
			cleanupQueued = true
		}

		return nil
	})
	s.metrics.RecordTransaction(time.Since(transactionStartedAt), err)
	if err != nil {
		if len(stagedBlobs) == 0 {
			return err
		}

		confirmationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		outcome, confirmationErr := s.confirmPublishOutcome(confirmationCtx, stagedBlobs)
		cancel()
		switch outcome {
		case publishOutcomeCommitted:
			s.logger.Error(
				"activated storage blobs observed after transaction error; preserving committed state",
				zap.Int("blob_count", len(stagedBlobs)),
				zap.Error(err),
			)
			s.triggerCleanup(ctx)
			return errors.Join(err, fmt.Errorf("storage publish result is uncertain; activated blobs were preserved"))
		case publishOutcomeRolledBack:
			s.triggerCleanup(ctx)
			return err
		default:
			s.logger.Error(
				"storage publish outcome is uncertain; preserving tracked blobs for recovery",
				zap.Int("blob_count", len(stagedBlobs)),
				zap.Error(err),
				zap.NamedError("confirmation_error", confirmationErr),
			)
			return errors.Join(err, fmt.Errorf("confirm storage publish outcome: %w", confirmationErr))
		}
	}

	if cleanupQueued {
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
	return nil
}

func (s *BlobLifecycleService) confirmPublishOutcome(
	ctx context.Context,
	stagedBlobs []*StagedBlob,
) (publishOutcome, error) {
	expectedByID := make(map[string]*StagedBlob, len(stagedBlobs))
	ids := make([]string, 0, len(stagedBlobs))
	for _, staged := range stagedBlobs {
		if staged == nil || staged.ID == "" {
			return publishOutcomeUnknown, fmt.Errorf("staged blob identity is missing")
		}
		if _, exists := expectedByID[staged.ID]; exists {
			return publishOutcomeUnknown, fmt.Errorf("staged blob %s is duplicated", staged.ID)
		}
		expectedByID[staged.ID] = staged
		ids = append(ids, staged.ID)
	}

	var outcome publishOutcome
	err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		blobsByID := make(map[string]model.StorageBlob, len(ids))
		for start := 0; start < len(ids); start += blobLedgerWriteBatchSize {
			end := min(start+blobLedgerWriteBatchSize, len(ids))
			var blobs []model.StorageBlob
			if err := tx.WithContext(ctx).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id IN ?", ids[start:end]).
				Find(&blobs).Error; err != nil {
				return err
			}
			for _, blob := range blobs {
				blobsByID[blob.ID] = blob
			}
		}
		if len(blobsByID) != len(ids) {
			return fmt.Errorf("storage publish confirmation is missing blob rows")
		}

		allCommitted := true
		allRolledBack := true
		for _, id := range ids {
			blob := blobsByID[id]
			expected := expectedByID[id]
			committed := blob.Status == model.StorageBlobStatusActive &&
				blob.StoragePath == expected.StoragePath &&
				blob.Size == expected.Size &&
				blob.RefCount == 1 &&
				blob.StagingPath == nil
			rolledBack := blob.Status == model.StorageBlobStatusStaging &&
				blob.StoragePath == expected.StoragePath &&
				blob.StagingPath != nil &&
				*blob.StagingPath == expected.StagingPath &&
				blob.RefCount == 0
			allCommitted = allCommitted && committed
			allRolledBack = allRolledBack && rolledBack
		}

		switch {
		case allCommitted:
			outcome = publishOutcomeCommitted
			return nil
		case allRolledBack:
			now := time.Now().UTC()
			txRepo := s.blobRepo.WithDB(tx)
			if err := txRepo.SealStagingForCleanupBatch(ctx, ids, now, blobLedgerWriteBatchSize); err != nil {
				return err
			}
			jobs := make([]model.StorageCleanupJob, 0, len(stagedBlobs))
			for _, staged := range stagedBlobs {
				jobs = append(jobs, model.StorageCleanupJob{
					BlobID:      staged.ID,
					StoragePath: staged.StoragePath,
					NextRetryAt: now,
					LastError:   "storage publish transaction rolled back",
				})
			}
			if err := txRepo.EnqueueCleanupBatch(ctx, jobs, blobLedgerWriteBatchSize); err != nil {
				return err
			}
			outcome = publishOutcomeRolledBack
			return nil
		default:
			return fmt.Errorf("storage publish confirmation found mixed blob states")
		}
	})
	if err != nil {
		return publishOutcomeUnknown, err
	}
	return outcome, nil
}
