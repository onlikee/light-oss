package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
)

func (s *RecycleBinService) restoreObject(ctx context.Context, itemID uint64) (RecycleBinFailedItem, error) {
	failedItem := RecycleBinFailedItem{ID: itemID}

	err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		recycleRepo := s.recycleRepo.WithDB(tx)
		objectRepo := s.objectRepo.WithDB(tx)
		bucketRepo := s.bucketRepo.WithDB(tx)

		item, err := recycleRepo.Find(ctx, itemID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperrors.New(http.StatusNotFound, "recycle_bin_item_not_found", "recycle bin item not found")
			}

			return apperrors.Wrap(http.StatusInternalServerError, "recycle_bin_restore_failed", "failed to load recycle bin item", err)
		}

		failedItem.BucketName = item.BucketName
		failedItem.Path = recycleBinObjectPath(*item)

		groupItems, err := loadRecycleBinActionItems(ctx, recycleRepo, *item)
		if err != nil {
			return apperrors.Wrap(http.StatusInternalServerError, "recycle_bin_restore_failed", "failed to load recycle bin item group", err)
		}

		exists, err := bucketRepo.Exists(ctx, item.BucketName)
		if err != nil {
			return apperrors.Wrap(http.StatusInternalServerError, "bucket_lookup_failed", "failed to look up bucket", err)
		}
		if !exists {
			return apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
		}

		restoreTargets := make([]model.RecycleBinObject, 0, len(groupItems))
		restoreKeys := make([]string, 0, len(groupItems))
		for _, groupItem := range groupItems {
			if shouldSkipRecycleBinRestoreItem(groupItem) {
				continue
			}

			restoreTargets = append(restoreTargets, groupItem)
			restoreKeys = append(restoreKeys, groupItem.ObjectKey)
		}

		existingKeys, err := objectRepo.ListExistingActiveKeys(ctx, item.BucketName, restoreKeys)
		if err != nil {
			return apperrors.Wrap(http.StatusInternalServerError, "object_lookup_failed", "failed to look up object", err)
		}
		if len(existingKeys) > 0 {
			return apperrors.New(http.StatusConflict, "object_exists", "object already exists")
		}

		restoredObjects := make([]model.Object, 0, len(restoreTargets))
		updatedAt := time.Now().UTC()
		for _, restoreTarget := range restoreTargets {
			restoredObjects = append(restoredObjects, model.Object{
				BucketName:       restoreTarget.BucketName,
				ObjectKey:        restoreTarget.ObjectKey,
				OriginalFilename: restoreTarget.OriginalFilename,
				StoragePath:      restoreTarget.StoragePath,
				Size:             restoreTarget.Size,
				ContentType:      restoreTarget.ContentType,
				ETag:             restoreTarget.ETag,
				FileFingerprint:  restoreTarget.FileFingerprint,
				Visibility:       restoreTarget.Visibility,
				CreatedAt:        restoreTarget.CreatedAt,
				UpdatedAt:        updatedAt,
			})
		}

		if len(restoredObjects) > 0 {
			if err := tx.WithContext(ctx).Create(&restoredObjects).Error; err != nil {
				if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateError(err) {
					return apperrors.New(http.StatusConflict, "object_exists", "object already exists")
				}
				if isForeignKeyError(err) {
					return apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
				}

				return apperrors.Wrap(http.StatusInternalServerError, "recycle_bin_restore_failed", "failed to restore object", err)
			}
		}

		deleted, err := recycleRepo.HardDeleteByIDs(ctx, recycleBinObjectIDs(groupItems))
		if err != nil {
			return apperrors.Wrap(http.StatusInternalServerError, "recycle_bin_restore_failed", "failed to remove recycle bin item", err)
		}
		if deleted != int64(len(groupItems)) {
			return apperrors.New(http.StatusNotFound, "recycle_bin_item_not_found", "recycle bin item not found")
		}

		return nil
	})
	return failedItem, err
}

func (s *RecycleBinService) deleteObject(ctx context.Context, itemID uint64) (RecycleBinFailedItem, error) {
	failedItem := RecycleBinFailedItem{ID: itemID}

	err := s.blobs.Publish(ctx, nil, func(tx *gorm.DB) ([]string, error) {
		recycleRepo := s.recycleRepo.WithDB(tx)

		item, err := recycleRepo.Find(ctx, itemID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, apperrors.New(http.StatusNotFound, "recycle_bin_item_not_found", "recycle bin item not found")
			}

			return nil, apperrors.Wrap(http.StatusInternalServerError, "recycle_bin_delete_failed", "failed to load recycle bin item", err)
		}

		failedItem.BucketName = item.BucketName
		failedItem.Path = recycleBinObjectPath(*item)
		groupItems, err := loadRecycleBinActionItems(ctx, recycleRepo, *item)
		if err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "recycle_bin_delete_failed", "failed to load recycle bin item group", err)
		}
		storagePaths := recycleBinObjectStoragePaths(groupItems)

		deleted, err := recycleRepo.HardDeleteByIDs(ctx, recycleBinObjectIDs(groupItems))
		if err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "recycle_bin_delete_failed", "failed to delete recycle bin item", err)
		}
		if deleted != int64(len(groupItems)) {
			return nil, apperrors.New(http.StatusNotFound, "recycle_bin_item_not_found", "recycle bin item not found")
		}

		return storagePaths, nil
	})

	return failedItem, err
}

func validateRecycleBinItemIDs(itemIDs []uint64) ([]uint64, error) {
	if len(itemIDs) == 0 {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_request", "item_ids must contain at least one entry")
	}
	if len(itemIDs) > maxExplorerLimit {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_request", "item_ids must contain at most 200 entries")
	}

	seen := make(map[uint64]struct{}, len(itemIDs))
	normalized := make([]uint64, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		if itemID == 0 {
			return nil, apperrors.New(http.StatusBadRequest, "invalid_request", "item_ids must contain positive integers")
		}
		if _, exists := seen[itemID]; exists {
			continue
		}

		seen[itemID] = struct{}{}
		normalized = append(normalized, itemID)
	}

	return normalized, nil
}
