package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
)

const (
	defaultListLimit = 20
	maxListLimit     = 100
)

type UploadObjectInput struct {
	BucketName       string
	ObjectKey        string
	Visibility       string
	AllowOverwrite   bool
	OriginalFilename string
	ContentType      string
	Body             io.Reader
}

type ListObjectsInput struct {
	BucketName string
	Prefix     string
	Limit      int
	Cursor     string
}

type ListObjectsOutput struct {
	Items      []model.Object
	NextCursor string
}

type ObjectService struct {
	gormDB      *gorm.DB
	bucketRepo  *repository.BucketRepository
	objectRepo  *repository.ObjectRepository
	recycleRepo *repository.RecycleBinRepository
	storage     BlobReader
	blobs       *BlobLifecycleService
}

func NewObjectService(
	gormDB *gorm.DB,
	bucketRepo *repository.BucketRepository,
	objectRepo *repository.ObjectRepository,
	recycleRepo *repository.RecycleBinRepository,
	blobStore BlobReader,
	blobLifecycle *BlobLifecycleService,
) *ObjectService {
	return &ObjectService{
		gormDB:      gormDB,
		bucketRepo:  bucketRepo,
		objectRepo:  objectRepo,
		recycleRepo: recycleRepo,
		storage:     blobStore,
		blobs:       blobLifecycle,
	}
}

func (s *ObjectService) Upload(ctx context.Context, input UploadObjectInput) (*model.Object, error) {
	if err := ValidateBucketName(input.BucketName); err != nil {
		return nil, err
	}
	if err := ValidateUserObjectKey(input.ObjectKey); err != nil {
		return nil, err
	}

	visibility, err := ParseVisibility(input.Visibility)
	if err != nil {
		return nil, err
	}

	if err := s.ensureBucketExists(ctx, input.BucketName); err != nil {
		return nil, err
	}

	if !input.AllowOverwrite {
		exists, err := s.objectRepo.ExistsActive(ctx, input.BucketName, input.ObjectKey)
		if err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "object_lookup_failed", "failed to look up object", err)
		}
		if exists {
			return nil, apperrors.New(http.StatusConflict, "object_exists", "object already exists; set X-Allow-Overwrite=true to overwrite")
		}
	}

	staged, err := s.blobs.Stage(ctx, input.Body)
	if err != nil {
		return nil, stagedBlobStoreError(err)
	}

	object := &model.Object{
		BucketName:       input.BucketName,
		ObjectKey:        input.ObjectKey,
		OriginalFilename: SanitizeOriginalFilename(input.OriginalFilename),
		StoragePath:      staged.StoragePath,
		Size:             int64(staged.Size),
		ContentType:      NormalizeContentType(input.ContentType),
		ETag:             staged.ETag,
		Visibility:       visibility,
	}

	var saved *model.Object
	err = s.blobs.Publish(ctx, []*StagedBlob{staged}, func(tx *gorm.DB) ([]string, error) {
		objectRepo := s.objectRepo.WithDB(tx)
		releasedPaths := make([]string, 0, 1)
		if input.AllowOverwrite {
			existing, findErr := objectRepo.FindActiveForUpdate(ctx, input.BucketName, input.ObjectKey)
			if findErr == nil {
				releasedPaths = append(releasedPaths, existing.StoragePath)
			} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
				return nil, apperrors.Wrap(http.StatusInternalServerError, "object_lookup_failed", "failed to look up object", findErr)
			}
			saved, err = objectRepo.Upsert(ctx, object)
		} else {
			saved, err = objectRepo.Create(ctx, object)
		}
		if err != nil {
			if !input.AllowOverwrite && (errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateError(err)) {
				return nil, apperrors.New(http.StatusConflict, "object_exists", "object already exists; set X-Allow-Overwrite=true to overwrite")
			}
			if isForeignKeyError(err) {
				return nil, apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
			}
			return nil, apperrors.Wrap(http.StatusInternalServerError, "object_metadata_failed", "failed to save object metadata", err)
		}
		return releasedPaths, nil
	})
	if err != nil {
		return nil, err
	}

	return saved, nil
}

func (s *ObjectService) Open(ctx context.Context, bucketName string, objectKey string) (*model.Object, io.ReadCloser, error) {
	object, err := s.GetMetadata(ctx, bucketName, objectKey)
	if err != nil {
		return nil, nil, err
	}

	reader, err := s.storage.Open(object.StoragePath)
	if err != nil {
		return nil, nil, apperrors.Wrap(http.StatusInternalServerError, "object_open_failed", "failed to open object content", err)
	}

	return object, reader, nil
}

func (s *ObjectService) GetMetadata(ctx context.Context, bucketName string, objectKey string) (*model.Object, error) {
	if err := ValidateBucketName(bucketName); err != nil {
		return nil, err
	}
	if err := ValidateObjectKey(objectKey); err != nil {
		return nil, err
	}

	object, err := s.objectRepo.FindActive(ctx, bucketName, objectKey)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.New(http.StatusNotFound, "object_not_found", "object not found")
		}

		return nil, apperrors.Wrap(http.StatusInternalServerError, "object_lookup_failed", "failed to look up object", err)
	}

	return object, nil
}

func (s *ObjectService) List(ctx context.Context, input ListObjectsInput) (*ListObjectsOutput, error) {
	if err := ValidateBucketName(input.BucketName); err != nil {
		return nil, err
	}
	if err := ValidatePrefix(input.Prefix); err != nil {
		return nil, err
	}
	if err := s.ensureBucketExists(ctx, input.BucketName); err != nil {
		return nil, err
	}

	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100")
	}

	cursor, err := decodeCursor(input.Cursor)
	if err != nil {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
	}

	objects, err := s.objectRepo.ListActive(ctx, repository.ListObjectsParams{
		BucketName: input.BucketName,
		Prefix:     input.Prefix,
		Limit:      limit + 1,
		Cursor:     cursor,
	})
	if err != nil {
		return nil, apperrors.Wrap(http.StatusInternalServerError, "object_list_failed", "failed to list objects", err)
	}

	nextCursor := ""
	if len(objects) > limit {
		last := objects[limit-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
		objects = objects[:limit]
	}

	return &ListObjectsOutput{
		Items:      objects,
		NextCursor: nextCursor,
	}, nil
}

func (s *ObjectService) Delete(ctx context.Context, bucketName string, objectKey string) error {
	if err := ValidateBucketName(bucketName); err != nil {
		return err
	}
	if err := ValidateObjectKey(objectKey); err != nil {
		return err
	}

	err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		objectRepo := s.objectRepo.WithDB(tx)
		recycleRepo := s.recycleRepo.WithDB(tx)

		object, err := objectRepo.FindActiveForUpdate(ctx, bucketName, objectKey)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return gorm.ErrRecordNotFound
			}

			return apperrors.Wrap(http.StatusInternalServerError, "object_lookup_failed", "failed to look up object", err)
		}

		deletedAt := time.Now().UTC()
		deleteGroupID := newRecycleBinDeleteGroupID()
		if err := recycleRepo.CreateBatch(ctx, recycleBinObjectsFromObjects([]model.Object{*object}, deletedAt, deleteGroupID)); err != nil {
			return apperrors.Wrap(http.StatusInternalServerError, "object_delete_failed", "failed to move object to recycle bin", err)
		}

		deleted, err := objectRepo.HardDeleteByID(ctx, object.ID)
		if err != nil {
			return apperrors.Wrap(http.StatusInternalServerError, "object_delete_failed", "failed to delete object", err)
		}
		if !deleted {
			return gorm.ErrRecordNotFound
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.New(http.StatusNotFound, "object_not_found", "object not found")
		}
		if appErr := apperrors.From(err); appErr.Code != "internal_error" {
			return err
		}

		return apperrors.Wrap(http.StatusInternalServerError, "object_delete_failed", "failed to delete object", err)
	}

	return nil
}

func (s *ObjectService) UpdateVisibility(
	ctx context.Context,
	bucketName string,
	objectKey string,
	visibilityValue string,
) (*model.Object, error) {
	if err := ValidateBucketName(bucketName); err != nil {
		return nil, err
	}
	if err := ValidateObjectKey(objectKey); err != nil {
		return nil, err
	}

	visibility, err := ParseVisibility(visibilityValue)
	if err != nil {
		return nil, err
	}

	object, err := s.objectRepo.UpdateVisibility(ctx, bucketName, objectKey, visibility)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.New(http.StatusNotFound, "object_not_found", "object not found")
		}

		return nil, apperrors.Wrap(http.StatusInternalServerError, "object_update_failed", "failed to update object visibility", err)
	}

	return object, nil
}
