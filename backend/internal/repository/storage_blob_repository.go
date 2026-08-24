package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

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

		// Scan stores the GET_LOCK result schema on connection's statement. Start
		// the reconciliation work with an initialized fresh statement while
		// preserving the pinned MySQL connection that owns the advisory lock.
		return fn(r.WithDB(connection.Session(&gorm.Session{NewDB: true, Initialized: true})))
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
