package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"light-oss/backend/internal/model"
)

var ErrStorageQuotaExceeded = errors.New("storage quota exceeded")

const (
	storageReconciliationLockName           = "light_oss_storage_reconciliation"
	storageReconciliationLockReleaseTimeout = 5 * time.Second
)

type StorageBlobActivation struct {
	ID         string
	ActualSize uint64
}

type StorageBlobRepository struct {
	db *gorm.DB
}

func NewStorageBlobRepository(db *gorm.DB) *StorageBlobRepository {
	return &StorageBlobRepository{db: db}
}

func (r *StorageBlobRepository) WithDB(db *gorm.DB) *StorageBlobRepository {
	if db == nil {
		return r
	}

	return &StorageBlobRepository{db: db}
}

func (r *StorageBlobRepository) WithReconciliationLock(
	ctx context.Context,
	wait time.Duration,
	fn func(*StorageBlobRepository) error,
) error {
	if r.db.Dialector.Name() != "mysql" {
		return fn(r)
	}

	waitSeconds := int64(wait / time.Second)
	if waitSeconds < 0 {
		waitSeconds = 0
	}
	return r.db.WithContext(ctx).Connection(func(connection *gorm.DB) (resultErr error) {
		var acquired sql.NullInt64
		if err := connection.Raw(
			"SELECT GET_LOCK(?, ?)",
			storageReconciliationLockName,
			waitSeconds,
		).Scan(&acquired).Error; err != nil {
			return fmt.Errorf("acquire storage reconciliation lock: %w", err)
		}
		if !acquired.Valid || acquired.Int64 != 1 {
			return fmt.Errorf("storage reconciliation lock was not acquired within %s", wait)
		}

		defer func() {
			releaseCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				storageReconciliationLockReleaseTimeout,
			)
			defer cancel()

			var released sql.NullInt64
			if err := connection.WithContext(releaseCtx).Raw(
				"SELECT RELEASE_LOCK(?)",
				storageReconciliationLockName,
			).Scan(&released).Error; err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("release storage reconciliation lock: %w", err))
				return
			}
			if !released.Valid || released.Int64 != 1 {
				resultErr = errors.Join(resultErr, fmt.Errorf("storage reconciliation lock was not owned when released"))
			}
		}()

		return fn(r.WithDB(connection))
	})
}

func (r *StorageBlobRepository) Transaction(ctx context.Context, fn func(*StorageBlobRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(r.WithDB(tx))
	})
}

func (r *StorageBlobRepository) CreateStaging(ctx context.Context, blob *model.StorageBlob) error {
	return r.db.WithContext(ctx).Create(blob).Error
}

func (r *StorageBlobRepository) CreateStagingWithLease(
	ctx context.Context,
	blob *model.StorageBlob,
	lease time.Duration,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(blob).Error; err != nil {
			return err
		}
		return r.WithDB(tx).RenewStagingLease(ctx, []string{blob.ID}, lease, 1)
	})
}

func (r *StorageBlobRepository) CreateStagingBatch(
	ctx context.Context,
	blobs []model.StorageBlob,
	batchSize int,
) error {
	return r.createStagingBatch(ctx, blobs, batchSize, 0)
}

func (r *StorageBlobRepository) CreateStagingBatchWithLease(
	ctx context.Context,
	blobs []model.StorageBlob,
	batchSize int,
	lease time.Duration,
) error {
	if lease <= 0 {
		return fmt.Errorf("staging lease must be greater than zero")
	}
	return r.createStagingBatch(ctx, blobs, batchSize, lease)
}

func (r *StorageBlobRepository) createStagingBatch(
	ctx context.Context,
	blobs []model.StorageBlob,
	batchSize int,
	lease time.Duration,
) error {
	if len(blobs) == 0 {
		return nil
	}
	batchSize = normalizeStorageBlobBatchSize(batchSize)

	var totalReserved uint64
	var ids []string
	if lease > 0 {
		ids = make([]string, 0, len(blobs))
	}
	for index := range blobs {
		if ^uint64(0)-totalReserved < blobs[index].Size {
			return fmt.Errorf("storage blob reservation size overflow")
		}
		totalReserved += blobs[index].Size
		if lease > 0 {
			ids = append(ids, blobs[index].ID)
		}
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if totalReserved > 0 {
			result := tx.Model(&model.SystemStorageQuota{}).
				Where(
					"id = ? AND used_bytes <= max_bytes AND reserved_bytes <= max_bytes - used_bytes AND ? <= max_bytes - used_bytes - reserved_bytes",
					systemStorageQuotaRowID,
					totalReserved,
				).
				UpdateColumn("reserved_bytes", gorm.Expr("reserved_bytes + ?", totalReserved))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrStorageQuotaExceeded
			}
		}

		if err := tx.CreateInBatches(&blobs, batchSize).Error; err != nil {
			return err
		}
		if lease <= 0 {
			return nil
		}
		return r.WithDB(tx).RenewStagingLease(ctx, ids, lease, batchSize)
	})
}

func (r *StorageBlobRepository) FindByID(ctx context.Context, id string) (*model.StorageBlob, error) {
	var blob model.StorageBlob
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&blob).Error; err != nil {
		return nil, err
	}

	return &blob, nil
}

func (r *StorageBlobRepository) FindByStoragePath(ctx context.Context, storagePath string) (*model.StorageBlob, error) {
	var blob model.StorageBlob
	if err := r.db.WithContext(ctx).Where("storage_path = ?", storagePath).First(&blob).Error; err != nil {
		return nil, err
	}

	return &blob, nil
}

func (r *StorageBlobRepository) ManagedPathExists(ctx context.Context, managedPath string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&model.StorageBlob{}).
		Where("storage_path = ? OR staging_path = ?", managedPath, managedPath).
		Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}

func (r *StorageBlobRepository) ListAll(ctx context.Context) ([]model.StorageBlob, error) {
	var blobs []model.StorageBlob
	if err := r.db.WithContext(ctx).Order("storage_path ASC").Find(&blobs).Error; err != nil {
		return nil, err
	}

	return blobs, nil
}

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

func countStoragePaths(paths []string) map[string]uint64 {
	counts := make(map[string]uint64, len(paths))
	for _, storagePath := range paths {
		if storagePath != "" {
			counts[storagePath]++
		}
	}
	return counts
}

func normalizeStorageBlobBatchSize(batchSize int) int {
	if batchSize <= 0 {
		return 100
	}
	return batchSize
}

func addStorageLedgerBytes(total *uint64, size uint64) error {
	if ^uint64(0)-*total < size {
		return fmt.Errorf("storage blob ledger size overflow")
	}
	*total += size
	return nil
}
