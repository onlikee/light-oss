package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"light-oss/backend/internal/model"
)

func (r *StorageBlobRepository) EnqueueCleanup(ctx context.Context, blobID string, storagePath string, lastError string, now time.Time) error {
	job := model.StorageCleanupJob{
		BlobID:      blobID,
		StoragePath: storagePath,
		NextRetryAt: now,
		LastError:   lastError,
	}

	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "blob_id"}},
		DoNothing: true,
	}).Create(&job).Error
}

func (r *StorageBlobRepository) EnqueueCleanupBatch(ctx context.Context, jobs []model.StorageCleanupJob, batchSize int) error {
	if len(jobs) == 0 {
		return nil
	}
	batchSize = normalizeStorageBlobBatchSize(batchSize)

	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "blob_id"}},
		DoNothing: true,
	}).CreateInBatches(&jobs, batchSize).Error
}

func (r *StorageBlobRepository) ListClaimCandidates(ctx context.Context, now time.Time, limit int) ([]model.StorageCleanupJob, error) {
	var jobs []model.StorageCleanupJob
	err := r.db.WithContext(ctx).
		Where("next_retry_at <= ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", now, now).
		Order("next_retry_at ASC, id ASC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

func (r *StorageBlobRepository) ClaimCleanupJob(ctx context.Context, id uint64, owner string, now time.Time, leaseUntil time.Time) (bool, error) {
	result := r.db.WithContext(ctx).Model(&model.StorageCleanupJob{}).
		Where("id = ? AND next_retry_at <= ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", id, now, now).
		Updates(map[string]any{
			"lease_owner":      owner,
			"lease_expires_at": leaseUntil,
		})
	return result.RowsAffected == 1, result.Error
}

func (r *StorageBlobRepository) RenewCleanupJobLease(
	ctx context.Context,
	id uint64,
	owner string,
	leaseUntil time.Time,
) (bool, error) {
	result := r.db.WithContext(ctx).Model(&model.StorageCleanupJob{}).
		Where("id = ? AND lease_owner = ?", id, owner).
		UpdateColumn("lease_expires_at", leaseUntil)
	if result.Error != nil || result.RowsAffected == 1 {
		return result.RowsAffected == 1, result.Error
	}

	var matched int64
	err := r.db.WithContext(ctx).Model(&model.StorageCleanupJob{}).
		Where("id = ? AND lease_owner = ?", id, owner).
		Count(&matched).Error
	return matched == 1, err
}

func (r *StorageBlobRepository) FindClaimedCleanupJob(ctx context.Context, id uint64, owner string) (*model.StorageCleanupJob, error) {
	var job model.StorageCleanupJob
	if err := r.db.WithContext(ctx).
		Where("id = ? AND lease_owner = ?", id, owner).
		First(&job).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *StorageBlobRepository) FailCleanupJob(
	ctx context.Context,
	id uint64,
	owner string,
	errMessage string,
	nextRetryAt time.Time,
) error {
	result := r.db.WithContext(ctx).Model(&model.StorageCleanupJob{}).
		Where("id = ? AND lease_owner = ?", id, owner).
		Updates(map[string]any{
			"retry_count":      gorm.Expr("retry_count + 1"),
			"next_retry_at":    nextRetryAt,
			"last_error":       errMessage,
			"lease_owner":      nil,
			"lease_expires_at": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *StorageBlobRepository) CompleteCleanupJob(ctx context.Context, id uint64, owner string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job model.StorageCleanupJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND lease_owner = ?", id, owner).
			First(&job).Error; err != nil {
			return err
		}

		var blob model.StorageBlob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", job.BlobID).
			First(&blob).Error; err != nil {
			return err
		}
		if blob.Status == model.StorageBlobStatusActive || blob.RefCount != 0 {
			return fmt.Errorf("cannot clean referenced blob %s", blob.ID)
		}

		column := "used_bytes"
		if blob.StagingPath != nil && *blob.StagingPath != "" {
			column = "reserved_bytes"
		}
		if blob.Size > 0 {
			result := tx.Model(&model.SystemStorageQuota{}).
				Where(fmt.Sprintf("id = ? AND %s >= ?", column), systemStorageQuotaRowID, blob.Size).
				UpdateColumn(column, gorm.Expr(column+" - ?", blob.Size))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("storage quota usage for blob %s is inconsistent", blob.ID)
			}
		}

		return tx.Delete(&model.StorageBlob{}, "id = ?", blob.ID).Error
	})
}
