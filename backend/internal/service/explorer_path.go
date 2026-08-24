package service

import (
	"path"
	"strings"

	"light-oss/backend/internal/model"
)

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
