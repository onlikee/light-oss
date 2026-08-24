package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
)

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
