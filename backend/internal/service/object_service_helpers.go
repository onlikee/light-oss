package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
)

func encodeCursor(createdAt time.Time, id uint64) string {
	raw := fmt.Sprintf("%d|%d", createdAt.UTC().UnixNano(), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(value string) (*repository.Cursor, error) {
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

	return &repository.Cursor{
		CreatedAt: time.Unix(0, nanos).UTC(),
		ID:        id,
	}, nil
}

func NormalizeContentType(contentType string) string {
	trimmed := strings.TrimSpace(contentType)
	if trimmed == "" {
		return "application/octet-stream"
	}

	mediaType, params, err := mime.ParseMediaType(trimmed)
	if err != nil {
		return trimmed
	}
	if _, hasCharset := params["charset"]; hasCharset || !shouldAttachUTF8Charset(mediaType) {
		return trimmed
	}

	params["charset"] = "utf-8"
	normalized := mime.FormatMediaType(mediaType, params)
	if normalized == "" {
		return mediaType + "; charset=utf-8"
	}

	return normalized
}

func shouldAttachUTF8Charset(mediaType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(mediaType))
	if strings.HasPrefix(normalized, "text/") {
		return true
	}
	if strings.HasSuffix(normalized, "+json") || strings.HasSuffix(normalized, "+xml") {
		return true
	}

	switch normalized {
	case "application/json", "application/ld+json", "application/xml", "application/xhtml+xml", "image/svg+xml":
		return true
	default:
		return false
	}
}

func (s *ObjectService) ensureBucketExists(ctx context.Context, bucketName string) error {
	exists, err := s.bucketRepo.Exists(ctx, bucketName)
	if err != nil {
		return apperrors.Wrap(http.StatusInternalServerError, "bucket_lookup_failed", "failed to look up bucket", err)
	}
	if !exists {
		return apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
	}

	return nil
}

func (s *ObjectService) findActiveObject(ctx context.Context, bucketName string, objectKey string) (*model.Object, error) {
	object, err := s.objectRepo.FindActive(ctx, bucketName, objectKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}

		return nil, err
	}

	return object, nil
}

func (s *ObjectService) loadActiveObjectsByKeys(
	ctx context.Context,
	bucketName string,
	objectKeys []string,
) (map[string]model.Object, error) {
	objects, err := s.objectRepo.FindActiveByKeys(ctx, bucketName, objectKeys)
	if err != nil {
		return nil, err
	}

	result := make(map[string]model.Object, len(objects))
	for _, object := range objects {
		result[object.ObjectKey] = object
	}

	return result, nil
}

func storagePathsFromObjects(objects []model.Object) []string {
	paths := make([]string, 0, len(objects))
	for _, object := range objects {
		if object.StoragePath == "" {
			continue
		}

		paths = append(paths, object.StoragePath)
	}

	return paths
}

func objectIDs(objects []model.Object) []uint64 {
	ids := make([]uint64, 0, len(objects))
	for _, object := range objects {
		ids = append(ids, object.ID)
	}

	return ids
}

func recycleBinObjectsFromObjects(
	objects []model.Object,
	deletedAt time.Time,
	deleteGroupID string,
) []model.RecycleBinObject {
	items := make([]model.RecycleBinObject, 0, len(objects))
	for _, object := range objects {
		items = append(items, recycleBinObjectFromObject(object, deletedAt, deleteGroupID))
	}

	return items
}

func recycleBinObjectsFromFolderDelete(
	objects []model.Object,
	folderPath string,
	deletedAt time.Time,
	deleteGroupID string,
) []model.RecycleBinObject {
	if len(objects) == 0 {
		return nil
	}

	markerKey := folderPath + folderMarkerFilename
	items := make([]model.RecycleBinObject, 0, len(objects)+1)
	var representative *model.Object

	for index := range objects {
		object := objects[index]
		if object.ObjectKey == markerKey {
			representative = &objects[index]
			continue
		}

		items = append(items, recycleBinObjectFromObject(object, deletedAt, deleteGroupID))
	}

	if representative != nil {
		items = append(items, recycleBinObjectFromObject(*representative, deletedAt, deleteGroupID))
		return items
	}

	items = append(items, syntheticRecycleBinDirectoryObject(objects[0].BucketName, folderPath, deletedAt, deleteGroupID))
	return items
}

func recycleBinObjectFromObject(object model.Object, deletedAt time.Time, deleteGroupID string) model.RecycleBinObject {
	return model.RecycleBinObject{
		DeleteGroupID:    deleteGroupID,
		BucketName:       object.BucketName,
		ObjectKey:        object.ObjectKey,
		OriginalFilename: object.OriginalFilename,
		StoragePath:      object.StoragePath,
		Size:             object.Size,
		ContentType:      object.ContentType,
		ETag:             object.ETag,
		FileFingerprint:  object.FileFingerprint,
		Visibility:       object.Visibility,
		CreatedAt:        object.CreatedAt,
		DeletedAt:        deletedAt,
	}
}

func syntheticRecycleBinDirectoryObject(
	bucketName string,
	folderPath string,
	deletedAt time.Time,
	deleteGroupID string,
) model.RecycleBinObject {
	return model.RecycleBinObject{
		DeleteGroupID:    deleteGroupID,
		BucketName:       bucketName,
		ObjectKey:        folderPath + folderMarkerFilename,
		OriginalFilename: folderMarkerFilename,
		StoragePath:      "",
		Size:             0,
		ContentType:      "application/x-directory",
		ETag:             "",
		FileFingerprint:  nil,
		Visibility:       model.VisibilityPrivate,
		CreatedAt:        deletedAt,
		DeletedAt:        deletedAt,
	}
}

func newRecycleBinDeleteGroupID() string {
	return uuid.NewString()
}
