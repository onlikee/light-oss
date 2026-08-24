package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"light-oss/backend/internal/model"
)

func (r *StorageBlobRepository) RenewStagingLease(
	ctx context.Context,
	blobIDs []string,
	lease time.Duration,
	batchSize int,
) error {
	if len(blobIDs) == 0 {
		return nil
	}
	if lease <= 0 {
		return fmt.Errorf("staging lease must be greater than zero")
	}
	batchSize = normalizeStorageBlobBatchSize(batchSize)

	ids := make([]string, 0, len(blobIDs))
	seen := make(map[string]struct{}, len(blobIDs))
	for _, blobID := range blobIDs {
		if blobID == "" {
			return fmt.Errorf("staging lease blob id is required")
		}
		if _, exists := seen[blobID]; exists {
			continue
		}
		seen[blobID] = struct{}{}
		ids = append(ids, blobID)
	}

	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		result := r.db.WithContext(ctx).Model(&model.StorageBlob{}).
			Where("id IN ? AND status = ?", ids[start:end], model.StorageBlobStatusStaging).
			UpdateColumn("staging_lease_expires_at", r.stagingLeaseExpiryExpression(lease))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(end-start) {
			return fmt.Errorf("staging lease batch changed concurrently")
		}
	}
	return nil
}

func (r *StorageBlobRepository) stagingLeaseExpiryExpression(lease time.Duration) any {
	if r.db.Dialector.Name() == "mysql" {
		return gorm.Expr("TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6))", lease.Microseconds())
	}
	return gorm.Expr(
		"STRFTIME('%Y-%m-%d %H:%M:%f', 'now', ?)",
		fmt.Sprintf("+%.6f seconds", lease.Seconds()),
	)
}

func (r *StorageBlobRepository) Reserve(ctx context.Context, blobID string, bytes uint64) error {
	if bytes == 0 {
		return nil
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var blob model.StorageBlob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND status = ?", blobID, model.StorageBlobStatusStaging).
			First(&blob).Error; err != nil {
			return err
		}

		result := tx.Model(&model.SystemStorageQuota{}).
			Where(
				"id = ? AND used_bytes <= max_bytes AND reserved_bytes <= max_bytes - used_bytes AND ? <= max_bytes - used_bytes - reserved_bytes",
				systemStorageQuotaRowID,
				bytes,
			).
			UpdateColumn("reserved_bytes", gorm.Expr("reserved_bytes + ?", bytes))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrStorageQuotaExceeded
		}

		result = tx.Model(&model.StorageBlob{}).
			Where("id = ? AND status = ?", blobID, model.StorageBlobStatusStaging).
			UpdateColumn("size", gorm.Expr("size + ?", bytes))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}

		return nil
	})
}

func (r *StorageBlobRepository) ActivateStaging(ctx context.Context, blobID string, actualSize uint64) error {
	var blob model.StorageBlob
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", blobID).
		First(&blob).Error; err != nil {
		return err
	}

	if blob.Status != model.StorageBlobStatusStaging {
		return fmt.Errorf("cannot activate blob %s in status %s", blob.ID, blob.Status)
	}
	if actualSize > blob.Size {
		return fmt.Errorf("blob %s size %d exceeds reserved bytes %d", blob.ID, actualSize, blob.Size)
	}

	result := r.db.WithContext(ctx).Model(&model.SystemStorageQuota{}).
		Where("id = ? AND reserved_bytes >= ?", systemStorageQuotaRowID, blob.Size).
		Updates(map[string]any{
			"reserved_bytes": gorm.Expr("reserved_bytes - ?", blob.Size),
			"used_bytes":     gorm.Expr("used_bytes + ?", actualSize),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("storage quota reservation for blob %s is inconsistent", blob.ID)
	}

	return r.db.WithContext(ctx).Model(&model.StorageBlob{}).
		Where("id = ? AND status = ?", blob.ID, model.StorageBlobStatusStaging).
		Updates(map[string]any{
			"size":                     actualSize,
			"ref_count":                1,
			"status":                   model.StorageBlobStatusActive,
			"staging_path":             nil,
			"staging_lease_expires_at": nil,
		}).Error
}

func (r *StorageBlobRepository) ActivateStagingBatch(
	ctx context.Context,
	activations []StorageBlobActivation,
	batchSize int,
) error {
	if len(activations) == 0 {
		return nil
	}
	batchSize = normalizeStorageBlobBatchSize(batchSize)

	actualSizes := make(map[string]uint64, len(activations))
	ids := make([]string, 0, len(activations))
	for _, activation := range activations {
		if activation.ID == "" {
			return fmt.Errorf("storage blob activation id is required")
		}
		if _, exists := actualSizes[activation.ID]; exists {
			return fmt.Errorf("storage blob activation %s is duplicated", activation.ID)
		}
		actualSizes[activation.ID] = activation.ActualSize
		ids = append(ids, activation.ID)
	}

	blobsByID := make(map[string]model.StorageBlob, len(ids))
	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		var blobs []model.StorageBlob
		if err := r.db.WithContext(ctx).
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
		return gorm.ErrRecordNotFound
	}

	var totalReserved uint64
	var totalActual uint64
	for _, activation := range activations {
		blob := blobsByID[activation.ID]
		if blob.Status != model.StorageBlobStatusStaging {
			return fmt.Errorf("cannot activate blob %s in status %s", blob.ID, blob.Status)
		}
		if activation.ActualSize > blob.Size {
			return fmt.Errorf("blob %s size %d exceeds reserved bytes %d", blob.ID, activation.ActualSize, blob.Size)
		}
		if ^uint64(0)-totalReserved < blob.Size || ^uint64(0)-totalActual < activation.ActualSize {
			return fmt.Errorf("storage blob activation size overflow")
		}
		totalReserved += blob.Size
		totalActual += activation.ActualSize
	}

	result := r.db.WithContext(ctx).Model(&model.SystemStorageQuota{}).
		Where("id = ? AND reserved_bytes >= ?", systemStorageQuotaRowID, totalReserved).
		Updates(map[string]any{
			"reserved_bytes": gorm.Expr("reserved_bytes - ?", totalReserved),
			"used_bytes":     gorm.Expr("used_bytes + ?", totalActual),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("storage quota batch reservation is inconsistent")
	}

	for start := 0; start < len(activations); start += batchSize {
		end := min(start+batchSize, len(activations))
		batch := activations[start:end]
		batchIDs := make([]string, 0, len(batch))
		sizeCase := "CASE id"
		sizeArgs := make([]any, 0, len(batch)*2)
		for _, activation := range batch {
			batchIDs = append(batchIDs, activation.ID)
			sizeCase += " WHEN ? THEN ?"
			sizeArgs = append(sizeArgs, activation.ID, activation.ActualSize)
		}
		sizeCase += " END"

		result = r.db.WithContext(ctx).Model(&model.StorageBlob{}).
			Where("id IN ? AND status = ?", batchIDs, model.StorageBlobStatusStaging).
			Updates(map[string]any{
				"size":                     gorm.Expr(sizeCase, sizeArgs...),
				"ref_count":                1,
				"status":                   model.StorageBlobStatusActive,
				"staging_path":             nil,
				"staging_lease_expires_at": nil,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(batch)) {
			return fmt.Errorf("storage blob batch activation is inconsistent")
		}
	}

	return nil
}
