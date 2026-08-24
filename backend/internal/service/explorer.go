package service

import (
	"context"
	"sort"
	"strings"

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
