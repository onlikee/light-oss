package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
)

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
