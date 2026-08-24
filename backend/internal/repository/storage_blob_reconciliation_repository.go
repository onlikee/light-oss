package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"light-oss/backend/internal/model"
)

func (r *StorageBlobRepository) ExpireStagingForCleanup(
	ctx context.Context,
	before time.Time,
	limit int,
	now time.Time,
) (int, error) {
	limit = normalizeStorageBlobBatchSize(limit)
	expiredCount := 0
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		expiredCount, err = r.WithDB(tx).expireStagingForCleanup(ctx, before, limit, now)
		return err
	})
	return expiredCount, err
}

func (r *StorageBlobRepository) expireStagingForCleanup(
	ctx context.Context,
	before time.Time,
	limit int,
	now time.Time,
) (int, error) {
	var blobs []model.StorageBlob
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("status = ? AND created_at <= ?", model.StorageBlobStatusStaging, before).
		Where("staging_lease_expires_at IS NULL OR staging_lease_expires_at <= ?", now).
		Order("created_at ASC, id ASC").
		Limit(limit).
		Find(&blobs).Error; err != nil {
		return 0, err
	}
	if len(blobs) == 0 {
		return 0, nil
	}

	ids := make([]string, 0, len(blobs))
	jobs := make([]model.StorageCleanupJob, 0, len(blobs))
	for _, blob := range blobs {
		ids = append(ids, blob.ID)
		jobs = append(jobs, model.StorageCleanupJob{
			BlobID:      blob.ID,
			StoragePath: blob.StoragePath,
			NextRetryAt: now,
			LastError:   "staging reservation expired",
		})
	}

	result := r.db.WithContext(ctx).Model(&model.StorageBlob{}).
		Where("id IN ? AND status = ? AND created_at <= ?", ids, model.StorageBlobStatusStaging, before).
		Where("staging_lease_expires_at IS NULL OR staging_lease_expires_at <= ?", now).
		Updates(map[string]any{
			"status":                   model.StorageBlobStatusPendingDelete,
			"staging_lease_expires_at": nil,
			"updated_at":               now,
		})
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != int64(len(blobs)) {
		return 0, fmt.Errorf("expired staging batch changed concurrently")
	}

	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "blob_id"}},
		DoNothing: true,
	}).CreateInBatches(&jobs, limit).Error; err != nil {
		return 0, err
	}
	return len(blobs), nil
}

func (r *StorageBlobRepository) SealStagingForCleanupBatch(
	ctx context.Context,
	blobIDs []string,
	now time.Time,
	batchSize int,
) error {
	if len(blobIDs) == 0 {
		return nil
	}
	batchSize = normalizeStorageBlobBatchSize(batchSize)

	for start := 0; start < len(blobIDs); start += batchSize {
		end := min(start+batchSize, len(blobIDs))
		batch := blobIDs[start:end]
		result := r.db.WithContext(ctx).Model(&model.StorageBlob{}).
			Where("id IN ? AND status = ? AND ref_count = 0", batch, model.StorageBlobStatusStaging).
			Updates(map[string]any{
				"status":                   model.StorageBlobStatusPendingDelete,
				"staging_lease_expires_at": nil,
				"updated_at":               now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(batch)) {
			return fmt.Errorf("staging cleanup batch changed concurrently")
		}
	}

	return nil
}

func (r *StorageBlobRepository) RegisterOrphan(ctx context.Context, blob *model.StorageBlob) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(blob).Error; err != nil {
			return err
		}

		return tx.Model(&model.SystemStorageQuota{}).
			Where("id = ?", systemStorageQuotaRowID).
			UpdateColumn("used_bytes", gorm.Expr("used_bytes + ?", blob.Size)).Error
	})
}

func (r *StorageBlobRepository) ScheduleOrphanCleanup(ctx context.Context, id string, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var blob model.StorageBlob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", id).
			First(&blob).Error; err != nil {
			return err
		}
		if blob.RefCount != 0 {
			return fmt.Errorf("cannot schedule referenced storage blob %s for cleanup", blob.ID)
		}

		switch blob.Status {
		case model.StorageBlobStatusOrphaned:
			if blob.StagingPath != nil && *blob.StagingPath != "" {
				return fmt.Errorf("cannot schedule orphaned storage blob %s with a staging path", blob.ID)
			}
			result := tx.Model(&model.StorageBlob{}).
				Where("id = ? AND status = ? AND ref_count = 0", blob.ID, model.StorageBlobStatusOrphaned).
				Updates(map[string]any{
					"status":     model.StorageBlobStatusPendingDelete,
					"updated_at": now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("orphaned storage blob %s changed concurrently", blob.ID)
			}
		case model.StorageBlobStatusPendingDelete:
			// A repeated scheduling request only refreshes the persistent job.
		default:
			return fmt.Errorf("cannot schedule storage blob %s in status %s for orphan cleanup", blob.ID, blob.Status)
		}

		job := model.StorageCleanupJob{
			BlobID:      blob.ID,
			StoragePath: blob.StoragePath,
			NextRetryAt: now,
			LastError:   "",
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "blob_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"storage_path":     blob.StoragePath,
				"next_retry_at":    now,
				"last_error":       "",
				"lease_owner":      nil,
				"lease_expires_at": nil,
				"updated_at":       now,
			}),
		}).Create(&job).Error
	})
}
