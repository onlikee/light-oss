package repository

import (
	"context"

	"gorm.io/gorm/clause"

	"light-oss/backend/internal/model"
)

func (r *ObjectRepository) FindActive(ctx context.Context, bucketName string, objectKey string) (*model.Object, error) {
	var object model.Object
	err := r.db.WithContext(ctx).
		Where("bucket_name = ? AND object_key = ?", bucketName, objectKey).
		First(&object).Error
	if err != nil {
		return nil, err
	}

	return &object, nil
}

func (r *ObjectRepository) FindActiveForUpdate(ctx context.Context, bucketName string, objectKey string) (*model.Object, error) {
	var object model.Object
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("bucket_name = ? AND object_key = ?", bucketName, objectKey).
		First(&object).Error
	if err != nil {
		return nil, err
	}
	return &object, nil
}

func (r *ObjectRepository) ExistsActive(ctx context.Context, bucketName string, objectKey string) (bool, error) {
	var count int64

	err := r.db.WithContext(ctx).
		Model(&model.Object{}).
		Where("bucket_name = ? AND object_key = ?", bucketName, objectKey).
		Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (r *ObjectRepository) ListExistingActiveKeys(ctx context.Context, bucketName string, objectKeys []string) ([]string, error) {
	if len(objectKeys) == 0 {
		return []string{}, nil
	}

	var keys []string
	err := r.db.WithContext(ctx).
		Model(&model.Object{}).
		Where("bucket_name = ?", bucketName).
		Where("object_key IN ?", objectKeys).
		Pluck("object_key", &keys).Error
	if err != nil {
		return nil, err
	}

	return keys, nil
}

func (r *ObjectRepository) FindAnyActiveByFingerprint(ctx context.Context, fileFingerprint string) (*model.Object, error) {
	var object model.Object
	err := r.db.WithContext(ctx).
		Where("file_fingerprint = ?", fileFingerprint).
		Order("created_at DESC").
		First(&object).Error
	if err != nil {
		return nil, err
	}

	return &object, nil
}

func (r *ObjectRepository) ListActive(ctx context.Context, params ListObjectsParams) ([]model.Object, error) {
	var objects []model.Object

	query := r.db.WithContext(ctx).Model(&model.Object{}).
		Where("bucket_name = ?", params.BucketName)

	query = applyObjectKeyPrefixFilter(query, params.Prefix)
	if params.Cursor != nil {
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND id < ?)",
			params.Cursor.CreatedAt,
			params.Cursor.CreatedAt,
			params.Cursor.ID,
		)
	}

	err := query.
		Order("created_at DESC").
		Order("id DESC").
		Limit(params.Limit).
		Find(&objects).Error
	return objects, err
}

func (r *ObjectRepository) ListActiveByPrefixOrdered(ctx context.Context, bucketName string, prefix string) ([]model.Object, error) {
	var objects []model.Object

	query := r.db.WithContext(ctx).
		Where("bucket_name = ?", bucketName)

	query = applyObjectKeyPrefixFilter(query, prefix)

	err := query.
		Order("object_key ASC").
		Find(&objects).Error
	return objects, err
}

func (r *ObjectRepository) ListActiveKeys(ctx context.Context, bucketName string) ([]string, error) {
	var keys []string

	err := r.db.WithContext(ctx).
		Model(&model.Object{}).
		Where("bucket_name = ?", bucketName).
		Order("object_key ASC").
		Pluck("object_key", &keys).Error
	return keys, err
}

func (r *ObjectRepository) FindActiveByKeys(ctx context.Context, bucketName string, objectKeys []string) ([]model.Object, error) {
	if len(objectKeys) == 0 {
		return []model.Object{}, nil
	}

	var objects []model.Object
	err := r.db.WithContext(ctx).
		Where("bucket_name = ?", bucketName).
		Where("object_key IN ?", objectKeys).
		Find(&objects).Error
	if err != nil {
		return nil, err
	}

	return objects, nil
}

func (r *ObjectRepository) FindActiveByKeysForUpdate(ctx context.Context, bucketName string, objectKeys []string) ([]model.Object, error) {
	if len(objectKeys) == 0 {
		return []model.Object{}, nil
	}

	var objects []model.Object
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("bucket_name = ?", bucketName).
		Where("object_key IN ?", objectKeys).
		Find(&objects).Error
	return objects, err
}

func (r *ObjectRepository) ListActiveByPrefixForUpdateOrdered(ctx context.Context, bucketName string, prefix string) ([]model.Object, error) {
	var objects []model.Object

	query := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("bucket_name = ?", bucketName)

	query = applyObjectKeyPrefixFilter(query, prefix)

	err := query.
		Order("object_key ASC").
		Find(&objects).Error
	return objects, err
}

func (r *ObjectRepository) ListAllByBucket(ctx context.Context, bucketName string) ([]model.Object, error) {
	var objects []model.Object

	err := r.db.WithContext(ctx).
		Where("bucket_name = ?", bucketName).
		Order("id ASC").
		Find(&objects).Error
	return objects, err
}

func (r *ObjectRepository) ExistsActiveWithPrefix(ctx context.Context, bucketName string, prefix string) (bool, error) {
	var count int64

	query := r.db.WithContext(ctx).
		Model(&model.Object{}).
		Where("bucket_name = ?", bucketName)

	query = applyObjectKeyPrefixFilter(query, prefix)

	if err := query.Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}

func (r *ObjectRepository) ExistsActiveWithPrefixExceptKey(ctx context.Context, bucketName string, prefix string, excludedKey string) (bool, error) {
	var count int64

	query := r.db.WithContext(ctx).
		Model(&model.Object{}).
		Where("bucket_name = ?", bucketName).
		Where(objectKeyPrefixLikeClause, likePrefixPattern(prefix))

	if excludedKey != "" {
		query = query.Where("object_key <> ?", excludedKey)
	}

	if err := query.Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}

func (r *ObjectRepository) ExistsActiveByStoragePath(ctx context.Context, storagePath string) (bool, error) {
	var count int64

	err := r.db.WithContext(ctx).
		Model(&model.Object{}).
		Where("storage_path = ?", storagePath).
		Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}
