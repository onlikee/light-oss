package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/storage"
)

type StorageReconciliationStore interface {
	Identity(context.Context) (string, error)
	Stat(string) (os.FileInfo, error)
	WalkManaged(context.Context) ([]storage.ManagedFileInfo, error)
}

type StorageReconciliationReport struct {
	TrackedBlobs      int
	RegisteredOrphans int
	MissingActive     int
}

type StorageReconciler struct {
	logger     *zap.Logger
	store      StorageReconciliationStore
	blobRepo   *repository.StorageBlobRepository
	stagingTTL time.Duration
}

const storageReconciliationLockWait = 30 * time.Second

func NewStorageReconciler(
	logger *zap.Logger,
	store StorageReconciliationStore,
	blobRepo *repository.StorageBlobRepository,
	stagingTTL time.Duration,
) *StorageReconciler {
	if stagingTTL <= 0 {
		stagingTTL = defaultStagingTTL
	}
	return &StorageReconciler{
		logger:     logger,
		store:      store,
		blobRepo:   blobRepo,
		stagingTTL: stagingTTL,
	}
}

func (r *StorageReconciler) Reconcile(ctx context.Context) (*StorageReconciliationReport, error) {
	var report *StorageReconciliationReport
	err := r.blobRepo.WithReconciliationLock(ctx, storageReconciliationLockWait, func(lockedRepo *repository.StorageBlobRepository) error {
		storageID, err := r.store.Identity(ctx)
		if err != nil {
			return fmt.Errorf("read storage identity: %w", err)
		}
		newlyBound, err := lockedRepo.ClaimStorageIdentity(ctx, storageID)
		if err != nil {
			return err
		}

		report, err = r.reconcileLocked(ctx, lockedRepo)
		if err != nil && newlyBound && (report == nil || report.RegisteredOrphans == 0) {
			if releaseErr := lockedRepo.ReleaseStorageIdentity(ctx, storageID); releaseErr != nil {
				return errors.Join(err, fmt.Errorf("release failed storage identity binding: %w", releaseErr))
			}
		}
		return err
	})
	return report, err
}

func (r *StorageReconciler) reconcileLocked(
	ctx context.Context,
	blobRepo *repository.StorageBlobRepository,
) (*StorageReconciliationReport, error) {
	if err := blobRepo.MarkReconciliationStarted(ctx); err != nil {
		return nil, fmt.Errorf("mark storage reconciliation started: %w", err)
	}
	now, err := blobRepo.DatabaseTime(ctx)
	if err != nil {
		return nil, fmt.Errorf("read database time: %w", err)
	}
	if err := r.sealExpiredStaging(ctx, blobRepo, now); err != nil {
		return nil, fmt.Errorf("seal expired staging blobs: %w", err)
	}

	blobs, err := blobRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list storage blobs: %w", err)
	}

	trackedPaths := make(map[string]struct{}, len(blobs)*2)
	missingActive := make([]string, 0)
	for _, blob := range blobs {
		trackedPaths[blob.StoragePath] = struct{}{}
		if blob.StagingPath != nil && *blob.StagingPath != "" {
			trackedPaths[*blob.StagingPath] = struct{}{}
		}

		if blob.Status != model.StorageBlobStatusActive {
			continue
		}
		info, err := r.store.Stat(blob.StoragePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missingActive = append(missingActive, blob.StoragePath)
				continue
			}
			return nil, fmt.Errorf("stat active blob %s: %w", blob.StoragePath, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("active blob %s is not a regular file", blob.StoragePath)
		}
		if info.Size() < 0 || uint64(info.Size()) != blob.Size {
			return nil, fmt.Errorf("active blob %s physical size %d does not match ledger size %d", blob.StoragePath, info.Size(), blob.Size)
		}
	}
	report := &StorageReconciliationReport{
		TrackedBlobs:  len(blobs),
		MissingActive: len(missingActive),
	}
	if len(missingActive) > 0 {
		return report, fmt.Errorf("storage reconciliation found %d active blobs without physical content; first missing path: %s", len(missingActive), missingActive[0])
	}

	files, err := r.store.WalkManaged(ctx)
	if err != nil {
		return report, fmt.Errorf("walk managed storage: %w", err)
	}

	for _, file := range files {
		if _, tracked := trackedPaths[file.RelativePath]; tracked {
			continue
		}
		tracked, err := blobRepo.ManagedPathExists(ctx, file.RelativePath)
		if err != nil {
			return report, fmt.Errorf("check managed path %s: %w", file.RelativePath, err)
		}
		if tracked {
			continue
		}
		if file.Size < 0 {
			return report, fmt.Errorf("managed file %s has a negative size", file.RelativePath)
		}

		orphan := &model.StorageBlob{
			ID:          uuid.NewString(),
			StoragePath: file.RelativePath,
			Size:        uint64(file.Size),
			Status:      model.StorageBlobStatusOrphaned,
		}
		if err := blobRepo.RegisterOrphan(ctx, orphan); err != nil {
			existing, findErr := blobRepo.FindByStoragePath(ctx, file.RelativePath)
			if findErr == nil {
				if existing.ID == orphan.ID {
					report.RegisteredOrphans++
				}
				continue
			}
			return report, fmt.Errorf("register orphan %s: %w", file.RelativePath, err)
		}
		report.RegisteredOrphans++
		r.logger.Warn(
			"unreferenced managed file registered as orphan",
			zap.String("blob_id", orphan.ID),
			zap.String("storage_path", orphan.StoragePath),
			zap.Uint64("size_bytes", orphan.Size),
		)
	}

	if err := blobRepo.ValidateAndMarkReconciled(ctx, now); err != nil {
		return report, fmt.Errorf("validate storage ledger and mark reconciliation complete: %w", err)
	}
	r.logger.Info(
		"storage reconciliation completed",
		zap.Int("tracked_blobs", report.TrackedBlobs),
		zap.Int("registered_orphans", report.RegisteredOrphans),
	)
	return report, nil
}

func (r *StorageReconciler) sealExpiredStaging(
	ctx context.Context,
	blobRepo *repository.StorageBlobRepository,
	now time.Time,
) error {
	for {
		sealed, err := blobRepo.ExpireStagingForCleanup(
			ctx,
			now.Add(-r.stagingTTL),
			defaultCleanupBatchSize,
			now,
		)
		if err != nil {
			return err
		}
		if sealed < defaultCleanupBatchSize {
			return nil
		}
	}
}
