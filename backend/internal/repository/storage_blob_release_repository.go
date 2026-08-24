package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"light-oss/backend/internal/model"
)

func (r *StorageBlobRepository) ReleaseReferences(
	ctx context.Context,
	storagePaths []string,
	now time.Time,
	batchSize int,
) error {
	counts := countStoragePaths(storagePaths)
	if len(counts) == 0 {
		return nil
	}
	batchSize = normalizeStorageBlobBatchSize(batchSize)

	paths := make([]string, 0, len(counts))
	for storagePath := range counts {
		paths = append(paths, storagePath)
	}
	sort.Strings(paths)

	blobsByPath := make(map[string]model.StorageBlob, len(paths))
	for start := 0; start < len(paths); start += batchSize {
		end := min(start+batchSize, len(paths))
		var blobs []model.StorageBlob
		if err := r.db.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("storage_path IN ?", paths[start:end]).
			Find(&blobs).Error; err != nil {
			return err
		}
		for _, blob := range blobs {
			blobsByPath[blob.StoragePath] = blob
		}
	}
	if len(blobsByPath) != len(paths) {
		return gorm.ErrRecordNotFound
	}

	type referenceUpdate struct {
		ID       string
		RefCount uint64
		Status   model.StorageBlobStatus
	}
	updates := make([]referenceUpdate, 0, len(paths))
	jobs := make([]model.StorageCleanupJob, 0, len(paths))
	for _, storagePath := range paths {
		blob := blobsByPath[storagePath]
		count := counts[storagePath]
		if blob.Status != model.StorageBlobStatusActive || blob.RefCount < count {
			return fmt.Errorf("storage blob %s reference count is inconsistent", blob.ID)
		}

		remaining := blob.RefCount - count
		status := model.StorageBlobStatusActive
		needsJob := false
		if remaining == 0 {
			status = model.StorageBlobStatusPendingDelete
			needsJob = true
		}
		updates = append(updates, referenceUpdate{
			ID:       blob.ID,
			RefCount: remaining,
			Status:   status,
		})
		if needsJob {
			jobs = append(jobs, model.StorageCleanupJob{
				BlobID:      blob.ID,
				StoragePath: blob.StoragePath,
				NextRetryAt: now,
				LastError:   "",
			})
		}
	}

	for start := 0; start < len(updates); start += batchSize {
		end := min(start+batchSize, len(updates))
		batch := updates[start:end]
		ids := make([]string, 0, len(batch))
		refCountCase := "CASE id"
		statusCase := "CASE id"
		refCountArgs := make([]any, 0, len(batch)*2)
		statusArgs := make([]any, 0, len(batch)*2)
		for _, update := range batch {
			ids = append(ids, update.ID)
			refCountCase += " WHEN ? THEN ?"
			refCountArgs = append(refCountArgs, update.ID, update.RefCount)
			statusCase += " WHEN ? THEN ?"
			statusArgs = append(statusArgs, update.ID, update.Status)
		}
		refCountCase += " END"
		statusCase += " END"

		result := r.db.WithContext(ctx).Model(&model.StorageBlob{}).
			Where("id IN ? AND status = ?", ids, model.StorageBlobStatusActive).
			Updates(map[string]any{
				"ref_count": gorm.Expr(refCountCase, refCountArgs...),
				"status":    gorm.Expr(statusCase, statusArgs...),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(batch)) {
			return fmt.Errorf("storage blob reference batch changed concurrently")
		}
	}

	if len(jobs) > 0 {
		if err := r.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "blob_id"}},
				DoUpdates: clause.Assignments(map[string]any{
					"next_retry_at":    now,
					"last_error":       "",
					"lease_owner":      nil,
					"lease_expires_at": nil,
				}),
			}).
			CreateInBatches(&jobs, batchSize).Error; err != nil {
			return err
		}
	}

	return nil
}

func (r *StorageBlobRepository) ReleaseStaging(ctx context.Context, blobID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var blob model.StorageBlob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", blobID).
			First(&blob).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if blob.Status != model.StorageBlobStatusStaging {
			return fmt.Errorf("cannot release blob %s in status %s", blob.ID, blob.Status)
		}

		if blob.Size > 0 {
			result := tx.Model(&model.SystemStorageQuota{}).
				Where("id = ? AND reserved_bytes >= ?", systemStorageQuotaRowID, blob.Size).
				UpdateColumn("reserved_bytes", gorm.Expr("reserved_bytes - ?", blob.Size))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("storage quota reservation for blob %s is inconsistent", blob.ID)
			}
		}

		return tx.Delete(&model.StorageBlob{}, "id = ?", blob.ID).Error
	})
}

func (r *StorageBlobRepository) ReleaseStagingBatch(ctx context.Context, blobIDs []string, batchSize int) error {
	if len(blobIDs) == 0 {
		return nil
	}
	batchSize = normalizeStorageBlobBatchSize(batchSize)

	ids := make([]string, 0, len(blobIDs))
	seen := make(map[string]struct{}, len(blobIDs))
	for _, blobID := range blobIDs {
		if blobID == "" {
			continue
		}
		if _, exists := seen[blobID]; exists {
			continue
		}
		seen[blobID] = struct{}{}
		ids = append(ids, blobID)
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		foundIDs := make([]string, 0, len(ids))
		var totalReserved uint64
		for start := 0; start < len(ids); start += batchSize {
			end := min(start+batchSize, len(ids))
			var blobs []model.StorageBlob
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id IN ?", ids[start:end]).
				Find(&blobs).Error; err != nil {
				return err
			}
			for _, blob := range blobs {
				if blob.Status != model.StorageBlobStatusStaging {
					return fmt.Errorf("cannot release blob %s in status %s", blob.ID, blob.Status)
				}
				if ^uint64(0)-totalReserved < blob.Size {
					return fmt.Errorf("storage blob release size overflow")
				}
				totalReserved += blob.Size
				foundIDs = append(foundIDs, blob.ID)
			}
		}
		if len(foundIDs) == 0 {
			return nil
		}

		if totalReserved > 0 {
			result := tx.Model(&model.SystemStorageQuota{}).
				Where("id = ? AND reserved_bytes >= ?", systemStorageQuotaRowID, totalReserved).
				UpdateColumn("reserved_bytes", gorm.Expr("reserved_bytes - ?", totalReserved))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("storage quota batch reservation is inconsistent")
			}
		}

		for start := 0; start < len(foundIDs); start += batchSize {
			end := min(start+batchSize, len(foundIDs))
			result := tx.Delete(&model.StorageBlob{}, "id IN ?", foundIDs[start:end])
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != int64(end-start) {
				return fmt.Errorf("storage blob batch release is inconsistent")
			}
		}

		return nil
	})
}
