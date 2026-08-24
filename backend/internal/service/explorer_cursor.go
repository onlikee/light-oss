package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"light-oss/backend/internal/model"
)

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
