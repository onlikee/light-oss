package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"light-oss/backend/internal/model"
)

func (r *StorageBlobRepository) ValidateAndMarkReconciled(ctx context.Context, reconciledAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var quota model.SystemStorageQuota
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", systemStorageQuotaRowID).
			First(&quota).Error; err != nil {
			return err
		}

		var blobs []model.StorageBlob
		if err := tx.Order("storage_path ASC").Find(&blobs).Error; err != nil {
			return err
		}

		type storageReferenceCount struct {
			StoragePath    string
			ReferenceCount uint64
			MinimumSize    int64
			MaximumSize    int64
		}
		var references []storageReferenceCount
		if err := tx.Raw(`
			SELECT
				storage_path,
				COUNT(*) AS reference_count,
				MIN(size) AS minimum_size,
				MAX(size) AS maximum_size
			FROM (
				SELECT storage_path, size FROM objects WHERE storage_path <> ''
				UNION ALL
				SELECT storage_path, size FROM recycle_bin_objects WHERE storage_path <> ''
			) AS storage_references
			GROUP BY storage_path
			ORDER BY storage_path ASC
		`).Scan(&references).Error; err != nil {
			return err
		}
		var cleanupJobs []model.StorageCleanupJob
		if err := tx.Model(&model.StorageCleanupJob{}).
			Select("blob_id", "storage_path").
			Order("blob_id ASC").
			Find(&cleanupJobs).Error; err != nil {
			return err
		}
		cleanupByBlobID := make(map[string]model.StorageCleanupJob, len(cleanupJobs))
		for _, job := range cleanupJobs {
			cleanupByBlobID[job.BlobID] = job
		}

		blobsByPath := make(map[string]model.StorageBlob, len(blobs))
		blobsByID := make(map[string]model.StorageBlob, len(blobs))
		managedPaths := make(map[string]string, len(blobs)*2)
		for _, blob := range blobs {
			if blob.StoragePath == "" {
				return fmt.Errorf("storage blob %s has an empty storage path", blob.ID)
			}
			if ownerID, exists := managedPaths[blob.StoragePath]; exists {
				return fmt.Errorf("managed storage path %s belongs to blobs %s and %s", blob.StoragePath, ownerID, blob.ID)
			}
			managedPaths[blob.StoragePath] = blob.ID
			blobsByPath[blob.StoragePath] = blob
			blobsByID[blob.ID] = blob

			if blob.StagingPath == nil || *blob.StagingPath == "" {
				continue
			}
			if ownerID, exists := managedPaths[*blob.StagingPath]; exists {
				return fmt.Errorf("managed storage path %s belongs to blobs %s and %s", *blob.StagingPath, ownerID, blob.ID)
			}
			managedPaths[*blob.StagingPath] = blob.ID
		}

		referencesByPath := make(map[string]uint64, len(references))
		for _, reference := range references {
			blob, exists := blobsByPath[reference.StoragePath]
			if !exists {
				return fmt.Errorf("storage reference path %s has no storage blob ledger entry", reference.StoragePath)
			}
			if blob.Status != model.StorageBlobStatusActive {
				return fmt.Errorf("storage blob %s in status %s has %d metadata references", blob.ID, blob.Status, reference.ReferenceCount)
			}
			if reference.MinimumSize < 0 || reference.MaximumSize < 0 ||
				reference.MinimumSize != reference.MaximumSize || uint64(reference.MaximumSize) != blob.Size {
				return fmt.Errorf(
					"storage blob %s ledger size %d does not match metadata size range %d..%d",
					blob.ID,
					blob.Size,
					reference.MinimumSize,
					reference.MaximumSize,
				)
			}
			referencesByPath[reference.StoragePath] = reference.ReferenceCount
		}
		for _, job := range cleanupJobs {
			blob, exists := blobsByID[job.BlobID]
			if !exists {
				return fmt.Errorf("cleanup job references missing storage blob %s", job.BlobID)
			}
			if job.StoragePath != blob.StoragePath {
				return fmt.Errorf("cleanup job path %s does not match storage blob %s path %s", job.StoragePath, blob.ID, blob.StoragePath)
			}
		}

		var expectedUsed uint64
		var expectedReserved uint64
		for _, blob := range blobs {
			referenceCount := referencesByPath[blob.StoragePath]
			hasStagingPath := blob.StagingPath != nil && *blob.StagingPath != ""
			hasStagingLease := blob.StagingLeaseExpiresAt != nil

			switch blob.Status {
			case model.StorageBlobStatusActive:
				if _, exists := cleanupByBlobID[blob.ID]; exists {
					return fmt.Errorf("active storage blob %s has a cleanup job", blob.ID)
				}
				if hasStagingPath {
					return fmt.Errorf("active storage blob %s retains staging path %s", blob.ID, *blob.StagingPath)
				}
				if hasStagingLease {
					return fmt.Errorf("active storage blob %s retains a staging lease", blob.ID)
				}
				if referenceCount == 0 || blob.RefCount != referenceCount {
					return fmt.Errorf("active storage blob %s reference count %d does not match metadata references %d", blob.ID, blob.RefCount, referenceCount)
				}
				if err := addStorageLedgerBytes(&expectedUsed, blob.Size); err != nil {
					return err
				}
			case model.StorageBlobStatusStaging:
				if !hasStagingPath || !hasStagingLease || blob.RefCount != 0 || referenceCount != 0 {
					return fmt.Errorf("staging storage blob %s has inconsistent staging path or references", blob.ID)
				}
				if err := addStorageLedgerBytes(&expectedReserved, blob.Size); err != nil {
					return err
				}
			case model.StorageBlobStatusPendingDelete:
				if blob.RefCount != 0 || referenceCount != 0 {
					return fmt.Errorf("pending-delete storage blob %s still has references", blob.ID)
				}
				if hasStagingLease {
					return fmt.Errorf("pending-delete storage blob %s retains a staging lease", blob.ID)
				}
				if _, exists := cleanupByBlobID[blob.ID]; !exists {
					return fmt.Errorf("pending-delete storage blob %s has no cleanup job", blob.ID)
				}
				if hasStagingPath {
					if err := addStorageLedgerBytes(&expectedReserved, blob.Size); err != nil {
						return err
					}
				} else if err := addStorageLedgerBytes(&expectedUsed, blob.Size); err != nil {
					return err
				}
			case model.StorageBlobStatusOrphaned:
				if _, exists := cleanupByBlobID[blob.ID]; exists {
					return fmt.Errorf("orphaned storage blob %s has a cleanup job", blob.ID)
				}
				if hasStagingPath || hasStagingLease || blob.RefCount != 0 || referenceCount != 0 {
					return fmt.Errorf("orphaned storage blob %s has a staging path or references", blob.ID)
				}
				if err := addStorageLedgerBytes(&expectedUsed, blob.Size); err != nil {
					return err
				}
			default:
				return fmt.Errorf("storage blob %s has unsupported status %s", blob.ID, blob.Status)
			}
		}

		if quota.UsedBytes != expectedUsed || quota.ReservedBytes != expectedReserved {
			return fmt.Errorf(
				"storage quota ledger mismatch: used_bytes=%d expected=%d reserved_bytes=%d expected=%d",
				quota.UsedBytes,
				expectedUsed,
				quota.ReservedBytes,
				expectedReserved,
			)
		}

		result := tx.Model(&model.SystemStorageQuota{}).
			Where("id = ?", systemStorageQuotaRowID).
			Update("reconciled_at", reconciledAt)
		if result.Error != nil {
			return result.Error
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
}

func (r *StorageBlobRepository) MarkReconciliationStarted(ctx context.Context) error {
	result := r.db.WithContext(ctx).Model(&model.SystemStorageQuota{}).
		Where("id = ?", systemStorageQuotaRowID).
		UpdateColumn("reconciled_at", nil)
	if result.Error != nil || result.RowsAffected > 0 {
		return result.Error
	}

	var quota model.SystemStorageQuota
	if err := r.db.WithContext(ctx).
		Select("id").
		Where("id = ?", systemStorageQuotaRowID).
		First(&quota).Error; err != nil {
		return err
	}
	return nil
}

func (r *StorageBlobRepository) ClaimStorageIdentity(ctx context.Context, storageID string) (bool, error) {
	result := r.db.WithContext(ctx).Model(&model.SystemStorageQuota{}).
		Where("id = ? AND storage_id IS NULL", systemStorageQuotaRowID).
		UpdateColumn("storage_id", storageID)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 1 {
		return true, nil
	}

	var quota model.SystemStorageQuota
	if err := r.db.WithContext(ctx).
		Select("storage_id").
		Where("id = ?", systemStorageQuotaRowID).
		First(&quota).Error; err != nil {
		return false, err
	}
	if quota.StorageID == nil || *quota.StorageID != storageID {
		return false, fmt.Errorf("storage root identity does not match the database binding")
	}
	return false, nil
}

func (r *StorageBlobRepository) ReleaseStorageIdentity(ctx context.Context, storageID string) error {
	result := r.db.WithContext(ctx).Model(&model.SystemStorageQuota{}).
		Where("id = ? AND storage_id = ?", systemStorageQuotaRowID, storageID).
		UpdateColumn("storage_id", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("failed storage identity binding changed concurrently")
	}
	return nil
}

func (r *StorageBlobRepository) CleanupJobCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.StorageCleanupJob{}).Count(&count).Error
	return count, err
}

func (r *StorageBlobRepository) DatabaseTime(ctx context.Context) (time.Time, error) {
	if r.db.Dialector.Name() == "mysql" {
		var now time.Time
		if err := r.db.WithContext(ctx).Raw("SELECT UTC_TIMESTAMP(6)").Scan(&now).Error; err != nil {
			return time.Time{}, err
		}
		return now.UTC(), nil
	}

	var value string
	if err := r.db.WithContext(ctx).
		Raw("SELECT STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now')").
		Scan(&value).Error; err != nil {
		return time.Time{}, err
	}
	now, err := time.Parse("2006-01-02T15:04:05.000Z", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse database time %q: %w", value, err)
	}
	return now.UTC(), nil
}
