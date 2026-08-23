package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
)

type RecycleBinItemType string

const (
	RecycleBinItemTypeFile      RecycleBinItemType = "file"
	RecycleBinItemTypeDirectory RecycleBinItemType = "directory"
)

type RecycleBinObjectItem struct {
	ID               uint64
	Type             RecycleBinItemType
	BucketName       string
	Path             string
	Name             string
	ObjectKey        string
	OriginalFilename string
	Size             int64
	ContentType      string
	ETag             string
	Visibility       model.Visibility
	CreatedAt        time.Time
	DeletedAt        time.Time
}

type ListRecycleBinObjectsInput struct {
	BucketName string
	Limit      int
	Cursor     string
}

type ListRecycleBinObjectsOutput struct {
	Items      []RecycleBinObjectItem
	NextCursor string
}

type RecycleBinFailedItem struct {
	ID         uint64
	BucketName string
	Path       string
	Code       string
	Message    string
}

type RestoreRecycleBinObjectsOutput struct {
	RestoredCount int
	FailedCount   int
	FailedItems   []RecycleBinFailedItem
}

type DeleteRecycleBinObjectsOutput struct {
	DeletedCount int
	FailedCount  int
	FailedItems  []RecycleBinFailedItem
}

type recycleBinLogicalItem struct {
	Item       RecycleBinObjectItem
	LastCursor *repository.RecycleBinCursor
}

type recycleBinDirectoryKey struct {
	BucketName string
	Path       string
}

type RecycleBinService struct {
	gormDB      *gorm.DB
	bucketRepo  *repository.BucketRepository
	objectRepo  *repository.ObjectRepository
	recycleRepo *repository.RecycleBinRepository
	blobs       *BlobLifecycleService
}

func NewRecycleBinService(
	gormDB *gorm.DB,
	bucketRepo *repository.BucketRepository,
	objectRepo *repository.ObjectRepository,
	recycleRepo *repository.RecycleBinRepository,
	blobLifecycle *BlobLifecycleService,
) *RecycleBinService {
	return &RecycleBinService{
		gormDB:      gormDB,
		bucketRepo:  bucketRepo,
		objectRepo:  objectRepo,
		recycleRepo: recycleRepo,
		blobs:       blobLifecycle,
	}
}

func (s *RecycleBinService) ListObjects(ctx context.Context, input ListRecycleBinObjectsInput) (*ListRecycleBinObjectsOutput, error) {
	if strings.TrimSpace(input.BucketName) != "" {
		if err := ValidateBucketName(input.BucketName); err != nil {
			return nil, err
		}
	}

	limit := input.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	cursor, err := decodeRecycleBinCursor(input.Cursor)
	if err != nil {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
	}

	batchSize := limit + 1
	if batchSize < defaultListLimit {
		batchSize = defaultListLimit
	}

	items := make([]RecycleBinObjectItem, 0, limit)
	queryCursor := cursor
	var pageLastCursor *repository.RecycleBinCursor
	pendingRawItems := make([]model.RecycleBinObject, 0, batchSize)

	for {
		rawItems, listErr := s.recycleRepo.List(ctx, repository.ListRecycleBinObjectsParams{
			BucketName: input.BucketName,
			Limit:      batchSize,
			Cursor:     queryCursor,
		})
		if listErr != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "recycle_bin_list_failed", "failed to list recycle bin objects", listErr)
		}
		if len(rawItems) > 0 {
			pendingRawItems = append(pendingRawItems, rawItems...)
			queryCursor = recycleBinCursorFromObject(rawItems[len(rawItems)-1])
		}

		completeRawItems, remainingRawItems := splitCompleteRecycleBinRawItems(pendingRawItems, len(rawItems) == 0)
		for _, logicalItem := range recycleBinLogicalItemsFromRawItems(completeRawItems) {
			if len(items) == limit {
				nextCursor := ""
				if pageLastCursor != nil {
					nextCursor = encodeRecycleBinCursor(pageLastCursor.DeletedAt, pageLastCursor.ID)
				}

				return &ListRecycleBinObjectsOutput{
					Items:      items,
					NextCursor: nextCursor,
				}, nil
			}

			items = append(items, logicalItem.Item)
			pageLastCursor = logicalItem.LastCursor
		}

		pendingRawItems = remainingRawItems
		if len(rawItems) == 0 {
			break
		}
	}

	return &ListRecycleBinObjectsOutput{
		Items:      items,
		NextCursor: "",
	}, nil
}

func (s *RecycleBinService) RestoreObjects(ctx context.Context, itemIDs []uint64) (*RestoreRecycleBinObjectsOutput, error) {
	normalizedIDs, err := validateRecycleBinItemIDs(itemIDs)
	if err != nil {
		return nil, err
	}

	result := &RestoreRecycleBinObjectsOutput{
		FailedItems: make([]RecycleBinFailedItem, 0),
	}

	for _, itemID := range normalizedIDs {
		failedItem, restoreErr := s.restoreObject(ctx, itemID)
		if restoreErr == nil {
			result.RestoredCount++
			continue
		}

		if failedItem.ID == 0 {
			failedItem.ID = itemID
		}

		appErr := apperrors.From(restoreErr)
		failedItem.Code = appErr.Code
		failedItem.Message = appErr.Message
		result.FailedItems = append(result.FailedItems, failedItem)
	}

	result.FailedCount = len(result.FailedItems)
	return result, nil
}

func (s *RecycleBinService) DeleteObjects(ctx context.Context, itemIDs []uint64) (*DeleteRecycleBinObjectsOutput, error) {
	normalizedIDs, err := validateRecycleBinItemIDs(itemIDs)
	if err != nil {
		return nil, err
	}

	result := &DeleteRecycleBinObjectsOutput{
		FailedItems: make([]RecycleBinFailedItem, 0),
	}
	for _, itemID := range normalizedIDs {
		failedItem, deleteErr := s.deleteObject(ctx, itemID)
		if deleteErr == nil {
			result.DeletedCount++
			continue
		}

		if failedItem.ID == 0 {
			failedItem.ID = itemID
		}

		appErr := apperrors.From(deleteErr)
		failedItem.Code = appErr.Code
		failedItem.Message = appErr.Message
		result.FailedItems = append(result.FailedItems, failedItem)
	}

	result.FailedCount = len(result.FailedItems)
	return result, nil
}

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
				IsDeleted:        false,
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

func recycleBinObjectToItem(item model.RecycleBinObject) RecycleBinObjectItem {
	return RecycleBinObjectItem{
		ID:               item.ID,
		Type:             recycleBinObjectType(item),
		BucketName:       item.BucketName,
		Path:             recycleBinObjectPath(item),
		Name:             recycleBinObjectName(item),
		ObjectKey:        item.ObjectKey,
		OriginalFilename: item.OriginalFilename,
		Size:             item.Size,
		ContentType:      item.ContentType,
		ETag:             item.ETag,
		Visibility:       item.Visibility,
		CreatedAt:        item.CreatedAt,
		DeletedAt:        item.DeletedAt,
	}
}

func recycleBinObjectType(item model.RecycleBinObject) RecycleBinItemType {
	if isFolderMarkerKey(item.ObjectKey) {
		return RecycleBinItemTypeDirectory
	}

	return RecycleBinItemTypeFile
}

func recycleBinObjectPath(item model.RecycleBinObject) string {
	if isFolderMarkerKey(item.ObjectKey) {
		return strings.TrimSuffix(item.ObjectKey, folderMarkerFilename)
	}

	return item.ObjectKey
}

func recycleBinObjectName(item model.RecycleBinObject) string {
	entryPath := strings.TrimSuffix(recycleBinObjectPath(item), "/")
	if entryPath == "" {
		return item.OriginalFilename
	}

	return path.Base(entryPath)
}

func recycleBinCursorFromObject(item model.RecycleBinObject) *repository.RecycleBinCursor {
	return &repository.RecycleBinCursor{
		DeletedAt: item.DeletedAt,
		ID:        item.ID,
	}
}

func splitCompleteRecycleBinRawItems(
	items []model.RecycleBinObject,
	reachedEnd bool,
) ([]model.RecycleBinObject, []model.RecycleBinObject) {
	if len(items) == 0 {
		return nil, nil
	}
	if reachedEnd {
		return items, nil
	}

	lastDeletedAt := items[len(items)-1].DeletedAt
	splitIndex := len(items)
	for splitIndex > 0 && items[splitIndex-1].DeletedAt.Equal(lastDeletedAt) {
		splitIndex--
	}

	return items[:splitIndex], items[splitIndex:]
}

func recycleBinLogicalItemsFromRawItems(items []model.RecycleBinObject) []recycleBinLogicalItem {
	logicalItems := make([]recycleBinLogicalItem, 0, len(items))

	for start := 0; start < len(items); {
		end := start + 1
		for end < len(items) && items[end].DeletedAt.Equal(items[start].DeletedAt) {
			end++
		}

		logicalItems = append(logicalItems, recycleBinLogicalItemsFromDeletedAtBatch(items[start:end])...)
		start = end
	}

	return logicalItems
}

func recycleBinLogicalItemsFromDeletedAtBatch(items []model.RecycleBinObject) []recycleBinLogicalItem {
	logicalItems := make([]recycleBinLogicalItem, 0, len(items))
	itemsByDeleteGroupID := make(map[string][]model.RecycleBinObject)
	deleteGroupIDs := make([]string, 0)

	for _, item := range items {
		if _, exists := itemsByDeleteGroupID[item.DeleteGroupID]; !exists {
			deleteGroupIDs = append(deleteGroupIDs, item.DeleteGroupID)
		}
		itemsByDeleteGroupID[item.DeleteGroupID] = append(itemsByDeleteGroupID[item.DeleteGroupID], item)
	}

	for _, deleteGroupID := range deleteGroupIDs {
		logicalItems = append(logicalItems, recycleBinLogicalItemsFromDeleteGroup(itemsByDeleteGroupID[deleteGroupID])...)
	}

	return logicalItems
}

func recycleBinLogicalItemsFromDeleteGroup(items []model.RecycleBinObject) []recycleBinLogicalItem {
	logicalItems := make([]recycleBinLogicalItem, 0, len(items))
	directoryMarkers := make(map[recycleBinDirectoryKey]model.RecycleBinObject)
	logicalItemIndexByDirectory := make(map[recycleBinDirectoryKey]int)

	for _, item := range items {
		if recycleBinObjectType(item) != RecycleBinItemTypeDirectory {
			continue
		}

		key := recycleBinDirectoryKey{
			BucketName: item.BucketName,
			Path:       recycleBinObjectPath(item),
		}
		directoryMarkers[key] = item
	}

	for _, item := range items {
		directoryKey, grouped := recycleBinOwningDirectoryKey(directoryMarkers, item)
		if !grouped {
			logicalItems = append(logicalItems, recycleBinLogicalItem{
				Item:       recycleBinObjectToItem(item),
				LastCursor: recycleBinCursorFromObject(item),
			})
			continue
		}

		logicalIndex, exists := logicalItemIndexByDirectory[directoryKey]
		if !exists {
			directoryMarker := directoryMarkers[directoryKey]
			logicalItems = append(logicalItems, recycleBinLogicalItem{
				Item:       recycleBinObjectToItem(directoryMarker),
				LastCursor: recycleBinCursorFromObject(item),
			})
			logicalIndex = len(logicalItems) - 1
			logicalItemIndexByDirectory[directoryKey] = logicalIndex
		} else {
			logicalItems[logicalIndex].LastCursor = recycleBinCursorFromObject(item)
		}

		if recycleBinObjectType(item) == RecycleBinItemTypeFile {
			logicalItems[logicalIndex].Item.Size += item.Size
		}
	}

	return logicalItems
}

func recycleBinOwningDirectoryKey(
	directoryMarkers map[recycleBinDirectoryKey]model.RecycleBinObject,
	item model.RecycleBinObject,
) (recycleBinDirectoryKey, bool) {
	itemPath := recycleBinObjectPath(item)
	includeSelf := recycleBinObjectType(item) == RecycleBinItemTypeDirectory

	for _, candidatePath := range recycleBinAncestorDirectoryPaths(itemPath, includeSelf) {
		key := recycleBinDirectoryKey{
			BucketName: item.BucketName,
			Path:       candidatePath,
		}
		if _, exists := directoryMarkers[key]; exists {
			return key, true
		}
	}

	return recycleBinDirectoryKey{}, false
}

func recycleBinAncestorDirectoryPaths(itemPath string, includeSelf bool) []string {
	trimmedPath := strings.TrimSuffix(itemPath, "/")
	if trimmedPath == "" {
		return nil
	}

	segments := strings.Split(trimmedPath, "/")
	maxDepth := len(segments) - 1
	if includeSelf {
		maxDepth = len(segments)
	}
	if maxDepth <= 0 {
		return nil
	}

	paths := make([]string, 0, maxDepth)
	for depth := 1; depth <= maxDepth; depth++ {
		paths = append(paths, strings.Join(segments[:depth], "/")+"/")
	}

	return paths
}

func loadRecycleBinActionItems(
	ctx context.Context,
	recycleRepo *repository.RecycleBinRepository,
	item model.RecycleBinObject,
) ([]model.RecycleBinObject, error) {
	if recycleBinObjectType(item) != RecycleBinItemTypeDirectory {
		return []model.RecycleBinObject{item}, nil
	}

	return recycleRepo.ListByDeleteGroupID(ctx, item.DeleteGroupID)
}

func shouldSkipRecycleBinRestoreItem(item model.RecycleBinObject) bool {
	return recycleBinObjectType(item) == RecycleBinItemTypeDirectory && item.StoragePath == ""
}

func recycleBinObjectIDs(items []model.RecycleBinObject) []uint64 {
	ids := make([]uint64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	return ids
}

func recycleBinObjectStoragePaths(items []model.RecycleBinObject) []string {
	paths := make([]string, 0, len(items))
	for _, item := range items {
		if item.StoragePath == "" {
			continue
		}

		paths = append(paths, item.StoragePath)
	}

	return paths
}

func encodeRecycleBinCursor(deletedAt time.Time, id uint64) string {
	raw := fmt.Sprintf("%d|%d", deletedAt.UTC().UnixNano(), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeRecycleBinCursor(value string) (*repository.RecycleBinCursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(string(raw), "|")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid cursor")
	}

	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, err
	}
	id, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return nil, err
	}

	return &repository.RecycleBinCursor{
		DeletedAt: time.Unix(0, nanos).UTC(),
		ID:        id,
	}, nil
}
