package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"light-oss/backend/internal/model"
)

const systemStorageQuotaRowID uint64 = 1

var ErrStorageQuotaBelowUsage = errors.New("storage quota cannot be lower than current usage and reservations")

type StorageQuotaRepository struct {
	db *gorm.DB
}

func NewStorageQuotaRepository(db *gorm.DB) *StorageQuotaRepository {
	return &StorageQuotaRepository{db: db}
}

func (r *StorageQuotaRepository) WithDB(db *gorm.DB) *StorageQuotaRepository {
	if db == nil {
		return r
	}

	return &StorageQuotaRepository{db: db}
}

func (r *StorageQuotaRepository) Get(ctx context.Context) (*model.SystemStorageQuota, error) {
	var quota model.SystemStorageQuota
	err := r.db.WithContext(ctx).
		Where("id = ?", systemStorageQuotaRowID).
		First(&quota).Error
	if err != nil {
		return nil, err
	}

	return &quota, nil
}

func (r *StorageQuotaRepository) BindStorageIdentity(ctx context.Context, storageID string) error {
	storageID = strings.TrimSpace(storageID)
	if storageID == "" {
		return fmt.Errorf("storage identity is required")
	}

	result := r.db.WithContext(ctx).Model(&model.SystemStorageQuota{}).
		Where("id = ? AND (storage_id IS NULL OR storage_id = ?)", systemStorageQuotaRowID, storageID).
		UpdateColumn("storage_id", storageID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}

	quota, err := r.Get(ctx)
	if err != nil {
		return err
	}
	if quota.StorageID != nil && *quota.StorageID == storageID {
		return nil
	}
	return fmt.Errorf("storage root identity does not match the database binding")
}

func (r *StorageQuotaRepository) EnsureDefault(
	ctx context.Context,
	defaultMaxBytes uint64,
) (*model.SystemStorageQuota, error) {
	quota, err := r.Get(ctx)
	if err == nil {
		return quota, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	createErr := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoNothing: true,
		}).
		Create(&model.SystemStorageQuota{
			ID:        systemStorageQuotaRowID,
			MaxBytes:  defaultMaxBytes,
			CreatedAt: now,
			UpdatedAt: now,
		}).Error
	if createErr != nil {
		return nil, createErr
	}

	return r.Get(ctx)
}

func (r *StorageQuotaRepository) UpdateMaxBytes(
	ctx context.Context,
	maxBytes uint64,
) (*model.SystemStorageQuota, error) {
	result := r.db.WithContext(ctx).Model(&model.SystemStorageQuota{}).
		Where(
			"id = ? AND used_bytes <= ? AND reserved_bytes <= ? AND used_bytes + reserved_bytes <= ?",
			systemStorageQuotaRowID,
			maxBytes,
			maxBytes,
			maxBytes,
		).
		Updates(map[string]any{
			"max_bytes":  maxBytes,
			"updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		if _, err := r.Get(ctx); err != nil {
			return nil, fmt.Errorf("load storage quota after failed update: %w", err)
		}
		return nil, ErrStorageQuotaBelowUsage
	}

	return r.Get(ctx)
}
