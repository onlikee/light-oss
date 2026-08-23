package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
)

const (
	folderMarkerFilename = ".light-oss-folder"
	defaultExplorerLimit = 100
	maxExplorerLimit     = 200
)

type FolderNode struct {
	Path       string
	Name       string
	ParentPath string
}

type ExplorerEntryType string

const (
	ExplorerEntryTypeDirectory ExplorerEntryType = "directory"
	ExplorerEntryTypeFile      ExplorerEntryType = "file"
)

type ExplorerSortBy string

const (
	ExplorerSortByName      ExplorerSortBy = "name"
	ExplorerSortBySize      ExplorerSortBy = "size"
	ExplorerSortByCreatedAt ExplorerSortBy = "created_at"
)

type ExplorerSortOrder string

const (
	ExplorerSortOrderAsc  ExplorerSortOrder = "asc"
	ExplorerSortOrderDesc ExplorerSortOrder = "desc"
)

type ExplorerEntry struct {
	Type    ExplorerEntryType
	Path    string
	Name    string
	IsEmpty bool
	Object  *model.Object
}

type ListExplorerEntriesInput struct {
	BucketName string
	Prefix     string
	Search     string
	Limit      int
	Cursor     string
	SortBy     string
	SortOrder  string
}

type ListExplorerEntriesOutput struct {
	Items      []ExplorerEntry
	NextCursor string
}

type CreateFolderInput struct {
	BucketName string
	Prefix     string
	Name       string
}

func (s *ObjectService) ListFolders(ctx context.Context, bucketName string) ([]FolderNode, error) {
	if err := ValidateBucketName(bucketName); err != nil {
		return nil, err
	}
	if err := s.ensureBucketExists(ctx, bucketName); err != nil {
		return nil, err
	}

	keys, err := s.objectRepo.ListActiveKeys(ctx, bucketName)
	if err != nil {
		return nil, apperrors.Wrap(500, "folder_list_failed", "failed to list folders", err)
	}

	folderMap := map[string]FolderNode{}
	for _, key := range keys {
		folderPath := folderPathFromObjectKey(key)
		if folderPath == "" {
			continue
		}
		addFolderHierarchy(folderMap, folderPath)
	}

	items := make([]FolderNode, 0, len(folderMap))
	for _, node := range folderMap {
		items = append(items, node)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Path < items[j].Path
	})

	return items, nil
}

func (s *ObjectService) ListExplorerEntries(ctx context.Context, input ListExplorerEntriesInput) (*ListExplorerEntriesOutput, error) {
	if err := ValidateBucketName(input.BucketName); err != nil {
		return nil, err
	}
	if err := ValidateFolderPrefix(input.Prefix); err != nil {
		return nil, err
	}
	if err := s.ensureBucketExists(ctx, input.BucketName); err != nil {
		return nil, err
	}

	limit := input.Limit
	if limit <= 0 {
		limit = defaultExplorerLimit
	}
	if limit > maxExplorerLimit {
		limit = maxExplorerLimit
	}

	sortBy := normalizeExplorerSortBy(input.SortBy)
	sortOrder := normalizeExplorerSortOrder(input.SortOrder)

	cursor, err := decodeExplorerCursor(input.Cursor)
	if err != nil {
		return nil, apperrors.New(400, "invalid_cursor", "cursor is invalid")
	}
	if cursor != nil && (cursor.SortBy != sortBy || cursor.SortOrder != sortOrder) {
		return nil, apperrors.New(400, "invalid_cursor", "cursor is invalid")
	}

	objects, err := s.objectRepo.ListActiveByPrefixOrdered(ctx, input.BucketName, input.Prefix)
	if err != nil {
		return nil, apperrors.Wrap(500, "explorer_list_failed", "failed to list explorer entries", err)
	}

	directories := map[string]ExplorerEntry{}
	files := map[string]ExplorerEntry{}
	search := strings.ToLower(strings.TrimSpace(input.Search))

	for _, object := range objects {
		relative := strings.TrimPrefix(object.ObjectKey, input.Prefix)
		if relative == "" {
			continue
		}

		segments := strings.Split(relative, "/")
		if len(segments) == 1 {
			if isFolderMarkerKey(object.ObjectKey) {
				continue
			}

			name := segments[0]
			files[name] = ExplorerEntry{
				Type:   ExplorerEntryTypeFile,
				Path:   object.ObjectKey,
				Name:   name,
				Object: cloneObject(object),
			}
			continue
		}

		name := segments[0]
		entry := directories[name]
		entry.Type = ExplorerEntryTypeDirectory
		entry.Name = name
		entry.Path = input.Prefix + name + "/"
		entry.IsEmpty = entry.IsEmpty || len(segments) == 0
		if len(segments) == 2 && segments[1] == folderMarkerFilename {
			if _, exists := directories[name]; !exists {
				entry.IsEmpty = true
			}
		} else {
			entry.IsEmpty = false
		}
		directories[name] = entry
	}

	entries := make([]ExplorerEntry, 0, len(directories)+len(files))
	for _, entry := range directories {
		if matchesExplorerSearch(entry.Name, search) {
			entries = append(entries, entry)
		}
	}
	for _, entry := range files {
		if matchesExplorerSearch(entry.Name, search) {
			entries = append(entries, entry)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return compareExplorerEntries(entries[i], entries[j], sortBy, sortOrder) < 0
	})

	start := 0
	if cursor != nil {
		cursorEntry := explorerCursorToEntry(cursor)
		for index, entry := range entries {
			if compareExplorerEntries(
				entry,
				cursorEntry,
				sortBy,
				sortOrder,
			) > 0 {
				start = index
				break
			}
			start = len(entries)
		}
	}

	if start > len(entries) {
		start = len(entries)
	}

	entries = entries[start:]
	nextCursor := ""
	if len(entries) > limit {
		last := entries[limit-1]
		nextCursor = encodeExplorerCursor(explorerCursorFromEntry(last, sortBy, sortOrder))
		entries = entries[:limit]
	}

	return &ListExplorerEntriesOutput{
		Items:      entries,
		NextCursor: nextCursor,
	}, nil
}

func (s *ObjectService) CreateFolder(ctx context.Context, input CreateFolderInput) (*FolderNode, error) {
	if err := ValidateBucketName(input.BucketName); err != nil {
		return nil, err
	}
	if err := ValidateFolderPrefix(input.Prefix); err != nil {
		return nil, err
	}
	if err := ValidateFolderName(input.Name); err != nil {
		return nil, err
	}

	exists, err := s.bucketRepo.Exists(ctx, input.BucketName)
	if err != nil {
		return nil, apperrors.Wrap(500, "bucket_lookup_failed", "failed to look up bucket", err)
	}
	if !exists {
		return nil, apperrors.New(404, "bucket_not_found", "bucket not found")
	}

	if input.Prefix != "" {
		parentExists, err := s.objectRepo.ExistsActiveWithPrefix(ctx, input.BucketName, input.Prefix)
		if err != nil {
			return nil, apperrors.Wrap(500, "folder_lookup_failed", "failed to look up parent folder", err)
		}
		if !parentExists {
			return nil, apperrors.New(404, "folder_not_found", "folder not found")
		}
	}

	folderPath := input.Prefix + input.Name + "/"
	folderExists, err := s.objectRepo.ExistsActiveWithPrefix(ctx, input.BucketName, folderPath)
	if err != nil {
		return nil, apperrors.Wrap(500, "folder_lookup_failed", "failed to look up folder", err)
	}
	if folderExists {
		return nil, apperrors.New(409, "folder_exists", "folder already exists")
	}

	markerKey := folderPath + folderMarkerFilename
	if _, err := s.createInternalObject(ctx, internalObjectInput{
		BucketName:       input.BucketName,
		ObjectKey:        markerKey,
		OriginalFilename: folderMarkerFilename,
		ContentType:      "application/x-directory",
		Visibility:       model.VisibilityPrivate,
	}); err != nil {
		return nil, err
	}

	return &FolderNode{
		Path:       folderPath,
		Name:       input.Name,
		ParentPath: input.Prefix,
	}, nil
}

func (s *ObjectService) DeleteFolder(ctx context.Context, bucketName string, folderPath string, recursive bool) error {
	if err := ValidateBucketName(bucketName); err != nil {
		return err
	}
	if err := ValidateFolderPath(folderPath); err != nil {
		return err
	}

	if recursive {
		err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			objectRepo := s.objectRepo.WithDB(tx)
			recycleRepo := s.recycleRepo.WithDB(tx)

			objects, err := objectRepo.ListActiveByPrefixForUpdateOrdered(ctx, bucketName, folderPath)
			if err != nil {
				return apperrors.Wrap(500, "folder_lookup_failed", "failed to inspect folder", err)
			}
			if len(objects) == 0 {
				return gorm.ErrRecordNotFound
			}

			deletedAt := time.Now().UTC()
			deleteGroupID := newRecycleBinDeleteGroupID()
			if err := recycleRepo.CreateBatch(ctx, recycleBinObjectsFromFolderDelete(objects, folderPath, deletedAt, deleteGroupID)); err != nil {
				return apperrors.Wrap(500, "folder_delete_failed", "failed to move folder to recycle bin", err)
			}

			deleted, err := objectRepo.HardDeleteByIDs(ctx, objectIDs(objects))
			if err != nil {
				return apperrors.Wrap(500, "folder_delete_failed", "failed to delete folder", err)
			}
			if deleted == 0 {
				return gorm.ErrRecordNotFound
			}

			return nil
		})
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperrors.New(404, "folder_not_found", "folder not found")
			}
			if appErr := apperrors.From(err); appErr.Code != "internal_error" {
				return err
			}

			return apperrors.Wrap(500, "folder_delete_failed", "failed to delete folder", err)
		}

		return nil
	}

	markerKey := folderPath + folderMarkerFilename
	err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		objectRepo := s.objectRepo.WithDB(tx)
		recycleRepo := s.recycleRepo.WithDB(tx)

		objects, err := objectRepo.ListActiveByPrefixForUpdateOrdered(ctx, bucketName, folderPath)
		if err != nil {
			return apperrors.Wrap(500, "folder_lookup_failed", "failed to inspect folder", err)
		}
		if len(objects) == 0 {
			return gorm.ErrRecordNotFound
		}

		var marker *model.Object
		for index := range objects {
			if objects[index].ObjectKey == markerKey {
				marker = &objects[index]
				continue
			}

			return apperrors.New(409, "folder_not_empty", "folder is not empty")
		}
		if marker == nil {
			return gorm.ErrRecordNotFound
		}

		deletedAt := time.Now().UTC()
		deleteGroupID := newRecycleBinDeleteGroupID()
		if err := recycleRepo.CreateBatch(ctx, recycleBinObjectsFromObjects([]model.Object{*marker}, deletedAt, deleteGroupID)); err != nil {
			return apperrors.Wrap(500, "folder_delete_failed", "failed to move folder to recycle bin", err)
		}

		deleted, err := objectRepo.HardDeleteByID(ctx, marker.ID)
		if err != nil {
			return apperrors.Wrap(500, "folder_delete_failed", "failed to delete folder", err)
		}
		if !deleted {
			return gorm.ErrRecordNotFound
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.New(404, "folder_not_found", "folder not found")
		}
		if appErr := apperrors.From(err); appErr.Code != "internal_error" {
			return err
		}

		return apperrors.Wrap(500, "folder_delete_failed", "failed to delete folder", err)
	}

	return nil
}

type internalObjectInput struct {
	BucketName       string
	ObjectKey        string
	OriginalFilename string
	ContentType      string
	Visibility       model.Visibility
}

func (s *ObjectService) createInternalObject(ctx context.Context, input internalObjectInput) (*model.Object, error) {
	staged, err := s.blobs.Stage(ctx, strings.NewReader(""))
	if err != nil {
		return nil, stagedBlobStoreError(err)
	}

	object := &model.Object{
		BucketName:       input.BucketName,
		ObjectKey:        input.ObjectKey,
		OriginalFilename: input.OriginalFilename,
		StoragePath:      staged.StoragePath,
		Size:             int64(staged.Size),
		ContentType:      input.ContentType,
		ETag:             staged.ETag,
		Visibility:       input.Visibility,
		IsDeleted:        false,
	}

	var saved *model.Object
	err = s.blobs.Publish(ctx, []*StagedBlob{staged}, func(tx *gorm.DB) ([]string, error) {
		saved, err = s.objectRepo.WithDB(tx).Create(ctx, object)
		if err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateError(err) {
				return nil, apperrors.New(http.StatusConflict, "folder_exists", "folder already exists")
			}
			if isForeignKeyError(err) {
				return nil, apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
			}
			return nil, apperrors.Wrap(500, "object_metadata_failed", "failed to save object metadata", err)
		}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}

	return saved, nil
}

type explorerCursor struct {
	SortBy    ExplorerSortBy    `json:"sort_by"`
	SortOrder ExplorerSortOrder `json:"sort_order"`
	Type      ExplorerEntryType `json:"type"`
	Name      string            `json:"name"`
	Size      *int64            `json:"size,omitempty"`
	CreatedAt *time.Time        `json:"created_at,omitempty"`
}

func encodeExplorerCursor(cursor explorerCursor) string {
	raw, err := json.Marshal(cursor)
	if err != nil {
		panic(fmt.Sprintf("marshal explorer cursor: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeExplorerCursor(value string) (*explorerCursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}

	var cursor explorerCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return nil, err
	}

	if !isValidExplorerSortBy(cursor.SortBy) || !isValidExplorerSortOrder(cursor.SortOrder) {
		return nil, fmt.Errorf("invalid cursor")
	}
	if cursor.Type != ExplorerEntryTypeDirectory && cursor.Type != ExplorerEntryTypeFile {
		return nil, fmt.Errorf("invalid cursor")
	}
	if cursor.Name == "" {
		return nil, fmt.Errorf("invalid cursor")
	}
	if cursor.Type == ExplorerEntryTypeFile {
		switch cursor.SortBy {
		case ExplorerSortBySize:
			if cursor.Size == nil {
				return nil, fmt.Errorf("invalid cursor")
			}
		case ExplorerSortByCreatedAt:
			if cursor.CreatedAt == nil {
				return nil, fmt.Errorf("invalid cursor")
			}
		}
	}

	return &cursor, nil
}

func compareExplorerEntries(
	left ExplorerEntry,
	right ExplorerEntry,
	sortBy ExplorerSortBy,
	sortOrder ExplorerSortOrder,
) int {
	if explorerTypeOrder(left.Type) != explorerTypeOrder(right.Type) {
		return explorerTypeOrder(left.Type) - explorerTypeOrder(right.Type)
	}

	if left.Type == ExplorerEntryTypeDirectory {
		return applyExplorerSortOrder(
			compareExplorerEntryNames(left.Name, right.Name),
			sortOrder,
		)
	}

	switch sortBy {
	case ExplorerSortBySize:
		if cmp := compareInt64(explorerEntrySize(left), explorerEntrySize(right)); cmp != 0 {
			return applyExplorerSortOrder(cmp, sortOrder)
		}
	case ExplorerSortByCreatedAt:
		if cmp := compareTime(explorerEntryCreatedAt(left), explorerEntryCreatedAt(right)); cmp != 0 {
			return applyExplorerSortOrder(cmp, sortOrder)
		}
	}

	return applyExplorerSortOrder(
		compareExplorerEntryNames(left.Name, right.Name),
		sortOrder,
	)
}

func explorerTypeOrder(entryType ExplorerEntryType) int {
	if entryType == ExplorerEntryTypeDirectory {
		return 0
	}
	return 1
}

func normalizeExplorerSortBy(value string) ExplorerSortBy {
	sortBy := ExplorerSortBy(strings.ToLower(strings.TrimSpace(value)))
	if !isValidExplorerSortBy(sortBy) {
		return ExplorerSortByCreatedAt
	}
	return sortBy
}

func isValidExplorerSortBy(value ExplorerSortBy) bool {
	return value == ExplorerSortByName || value == ExplorerSortBySize || value == ExplorerSortByCreatedAt
}

func normalizeExplorerSortOrder(value string) ExplorerSortOrder {
	sortOrder := ExplorerSortOrder(strings.ToLower(strings.TrimSpace(value)))
	if !isValidExplorerSortOrder(sortOrder) {
		return ExplorerSortOrderDesc
	}
	return sortOrder
}

func isValidExplorerSortOrder(value ExplorerSortOrder) bool {
	return value == ExplorerSortOrderAsc || value == ExplorerSortOrderDesc
}

func explorerCursorFromEntry(
	entry ExplorerEntry,
	sortBy ExplorerSortBy,
	sortOrder ExplorerSortOrder,
) explorerCursor {
	cursor := explorerCursor{
		SortBy:    sortBy,
		SortOrder: sortOrder,
		Type:      entry.Type,
		Name:      entry.Name,
	}
	if entry.Type != ExplorerEntryTypeFile || entry.Object == nil {
		return cursor
	}

	if sortBy == ExplorerSortBySize {
		size := entry.Object.Size
		cursor.Size = &size
	}
	if sortBy == ExplorerSortByCreatedAt {
		createdAt := entry.Object.CreatedAt
		cursor.CreatedAt = &createdAt
	}

	return cursor
}

func explorerCursorToEntry(cursor *explorerCursor) ExplorerEntry {
	entry := ExplorerEntry{
		Type: cursor.Type,
		Name: cursor.Name,
	}
	if cursor.Type != ExplorerEntryTypeFile {
		return entry
	}

	object := &model.Object{}
	if cursor.Size != nil {
		object.Size = *cursor.Size
	}
	if cursor.CreatedAt != nil {
		object.CreatedAt = *cursor.CreatedAt
	}
	entry.Object = object
	return entry
}

func applyExplorerSortOrder(value int, order ExplorerSortOrder) int {
	if order == ExplorerSortOrderDesc {
		return -value
	}
	return value
}

func compareExplorerEntryNames(left string, right string) int {
	leftName := strings.ToLower(left)
	rightName := strings.ToLower(right)
	if leftName != rightName {
		if leftName < rightName {
			return -1
		}
		return 1
	}

	return strings.Compare(left, right)
}

func explorerEntrySize(entry ExplorerEntry) int64 {
	if entry.Object == nil {
		return 0
	}
	return entry.Object.Size
}

func explorerEntryCreatedAt(entry ExplorerEntry) time.Time {
	if entry.Object == nil {
		return time.Time{}
	}
	return entry.Object.CreatedAt
}

func compareInt64(left int64, right int64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compareTime(left time.Time, right time.Time) int {
	if left.Before(right) {
		return -1
	}
	if left.After(right) {
		return 1
	}
	return 0
}

func matchesExplorerSearch(name string, search string) bool {
	if search == "" {
		return true
	}

	return strings.Contains(strings.ToLower(name), search)
}

func folderPathFromObjectKey(key string) string {
	dir := path.Dir(key)
	if dir == "." || dir == "/" {
		return ""
	}

	return strings.TrimPrefix(dir, "/") + "/"
}

func addFolderHierarchy(folderMap map[string]FolderNode, folderPath string) {
	trimmed := strings.TrimSuffix(folderPath, "/")
	if trimmed == "" {
		return
	}

	parts := strings.Split(trimmed, "/")
	current := ""
	for _, part := range parts {
		if current == "" {
			current = part + "/"
		} else {
			current += part + "/"
		}

		if _, exists := folderMap[current]; exists {
			continue
		}

		folderMap[current] = FolderNode{
			Path:       current,
			Name:       part,
			ParentPath: parentFolderPath(current),
		}
	}
}

func parentFolderPath(folderPath string) string {
	trimmed := strings.TrimSuffix(folderPath, "/")
	if trimmed == "" || !strings.Contains(trimmed, "/") {
		return ""
	}

	parent := path.Dir(trimmed)
	if parent == "." || parent == "/" {
		return ""
	}

	return strings.TrimPrefix(parent, "/") + "/"
}

func isFolderMarkerKey(key string) bool {
	return path.Base(key) == folderMarkerFilename
}

func cloneObject(object model.Object) *model.Object {
	copy := object
	return &copy
}
