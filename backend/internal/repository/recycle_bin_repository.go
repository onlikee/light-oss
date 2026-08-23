package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
)

type RecycleBinCursor struct {
	DeletedAt time.Time
	ID        uint64
}

type ListRecycleBinObjectsParams struct {
	BucketName string
	Limit      int
	Cursor     *RecycleBinCursor
}

type RecycleBinRepository struct {
	db *gorm.DB
}

func NewRecycleBinRepository(db *gorm.DB) *RecycleBinRepository {
	return &RecycleBinRepository{db: db}
}

func (r *RecycleBinRepository) WithDB(db *gorm.DB) *RecycleBinRepository {
	if db == nil {
		return r
	}

	return &RecycleBinRepository{db: db}
}

func (r *RecycleBinRepository) CreateBatch(ctx context.Context, items []model.RecycleBinObject) error {
	if len(items) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).Create(&items).Error
}

func (r *RecycleBinRepository) Find(ctx context.Context, id uint64) (*model.RecycleBinObject, error) {
	var item model.RecycleBinObject
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, err
	}

	return &item, nil
}

func (r *RecycleBinRepository) FindByIDs(ctx context.Context, ids []uint64) ([]model.RecycleBinObject, error) {
	if len(ids) == 0 {
		return []model.RecycleBinObject{}, nil
	}

	var items []model.RecycleBinObject
	err := r.db.WithContext(ctx).
		Where("id IN ?", ids).
		Order("deleted_at DESC").
		Order("id DESC").
		Find(&items).Error
	if err != nil {
		return nil, err
	}

	return items, nil
}

func (r *RecycleBinRepository) List(ctx context.Context, params ListRecycleBinObjectsParams) ([]model.RecycleBinObject, error) {
	var items []model.RecycleBinObject

	query := r.db.WithContext(ctx).Model(&model.RecycleBinObject{})
	if params.BucketName != "" {
		query = query.Where("bucket_name = ?", params.BucketName)
	}
	if params.Cursor != nil {
		query = query.Where(
			"(deleted_at < ?) OR (deleted_at = ? AND id < ?)",
			params.Cursor.DeletedAt,
			params.Cursor.DeletedAt,
			params.Cursor.ID,
		)
	}

	err := query.
		Order("deleted_at DESC").
		Order("id DESC").
		Limit(params.Limit).
		Find(&items).Error
	return items, err
}

func (r *RecycleBinRepository) ExistsByStoragePath(ctx context.Context, storagePath string) (bool, error) {
	var count int64

	err := r.db.WithContext(ctx).
		Model(&model.RecycleBinObject{}).
		Where("storage_path = ?", storagePath).
		Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (r *RecycleBinRepository) ListAllByBucket(ctx context.Context, bucketName string) ([]model.RecycleBinObject, error) {
	var items []model.RecycleBinObject

	err := r.db.WithContext(ctx).
		Where("bucket_name = ?", bucketName).
		Order("id ASC").
		Find(&items).Error
	if err != nil {
		return nil, err
	}

	return items, nil
}

func (r *RecycleBinRepository) ListByDeleteGroupID(
	ctx context.Context,
	deleteGroupID string,
) ([]model.RecycleBinObject, error) {
	var items []model.RecycleBinObject

	err := r.db.WithContext(ctx).
		Where("delete_group_id = ?", deleteGroupID).
		Order("id DESC").
		Find(&items).Error
	if err != nil {
		return nil, err
	}

	return items, nil
}

func (r *RecycleBinRepository) HardDelete(ctx context.Context, id uint64) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&model.RecycleBinObject{})
	return result.RowsAffected > 0, result.Error
}

func (r *RecycleBinRepository) HardDeleteByIDs(ctx context.Context, ids []uint64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	result := r.db.WithContext(ctx).
		Where("id IN ?", ids).
		Delete(&model.RecycleBinObject{})
	return result.RowsAffected, result.Error
}

func (r *RecycleBinRepository) HardDeleteByBucket(ctx context.Context, bucketName string) error {
	return r.db.WithContext(ctx).
		Where("bucket_name = ?", bucketName).
		Delete(&model.RecycleBinObject{}).Error
}
