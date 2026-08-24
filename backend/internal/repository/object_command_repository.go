package repository

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
)

func (r *ObjectRepository) HardDeleteByBucket(ctx context.Context, bucketName string) error {
	return r.db.WithContext(ctx).
		Where("bucket_name = ?", bucketName).
		Delete(&model.Object{}).Error
}

func (r *ObjectRepository) HardDelete(ctx context.Context, bucketName string, objectKey string) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("bucket_name = ? AND object_key = ?", bucketName, objectKey).
		Delete(&model.Object{})
	return result.RowsAffected > 0, result.Error
}

func (r *ObjectRepository) HardDeleteByID(ctx context.Context, id uint64) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&model.Object{})
	return result.RowsAffected > 0, result.Error
}

func (r *ObjectRepository) HardDeleteByIDs(ctx context.Context, ids []uint64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	result := r.db.WithContext(ctx).
		Where("id IN ?", ids).
		Delete(&model.Object{})
	return result.RowsAffected, result.Error
}

func (r *ObjectRepository) HardDeleteByPrefix(ctx context.Context, bucketName string, prefix string) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("bucket_name = ?", bucketName).
		Where(objectKeyPrefixLikeClause, likePrefixPattern(prefix)).
		Delete(&model.Object{})
	return result.RowsAffected, result.Error
}

func likePrefixPattern(prefix string) string {
	return escapeLikeValue(prefix) + "%"
}

func escapeLikeValue(value string) string {
	replacer := strings.NewReplacer(
		"!", "!!",
		"%", "!%",
		"_", "!_",
	)
	return replacer.Replace(value)
}

func applyObjectKeyPrefixFilter(query *gorm.DB, prefix string) *gorm.DB {
	if prefix == "" {
		return query
	}

	return query.Where(objectKeyPrefixLikeClause, likePrefixPattern(prefix))
}

func (r *ObjectRepository) UpdateVisibility(
	ctx context.Context,
	bucketName string,
	objectKey string,
	visibility model.Visibility,
) (*model.Object, error) {
	result := r.db.WithContext(ctx).Model(&model.Object{}).
		Where("bucket_name = ? AND object_key = ?", bucketName, objectKey).
		Updates(map[string]any{
			"visibility": visibility,
			"updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}

	return r.FindActive(ctx, bucketName, objectKey)
}
