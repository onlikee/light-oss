package service

import (
	"context"
	"errors"
	"net/http"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
)

type BucketService struct {
	bucketRepo  *repository.BucketRepository
	objectRepo  *repository.ObjectRepository
	recycleRepo *repository.RecycleBinRepository
	siteRepo    *repository.SiteRepository
	blobs       *BlobLifecycleService
}

func NewBucketService(
	bucketRepo *repository.BucketRepository,
	objectRepo *repository.ObjectRepository,
	recycleRepo *repository.RecycleBinRepository,
	siteRepo *repository.SiteRepository,
	blobLifecycle *BlobLifecycleService,
) *BucketService {
	return &BucketService{
		bucketRepo:  bucketRepo,
		objectRepo:  objectRepo,
		recycleRepo: recycleRepo,
		siteRepo:    siteRepo,
		blobs:       blobLifecycle,
	}
}

func (s *BucketService) Create(ctx context.Context, name string) (*model.Bucket, error) {
	if err := ValidateBucketName(name); err != nil {
		return nil, err
	}

	bucket := &model.Bucket{Name: name}
	if err := s.bucketRepo.Create(ctx, bucket); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateError(err) {
			return nil, apperrors.New(http.StatusConflict, "bucket_exists", "bucket already exists")
		}

		return nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_create_failed", "failed to create bucket", err)
	}

	return bucket, nil
}

func (s *BucketService) List(ctx context.Context, search string) ([]model.Bucket, error) {
	buckets, err := s.bucketRepo.List(ctx, search)
	if err != nil {
		return nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_list_failed", "failed to list buckets", err)
	}

	return buckets, nil
}

func (s *BucketService) Delete(ctx context.Context, name string) error {
	if err := ValidateBucketName(name); err != nil {
		return err
	}

	err := s.blobs.Publish(ctx, nil, func(tx *gorm.DB) ([]string, error) {
		bucketRepo := s.bucketRepo.WithDB(tx)
		objectRepo := s.objectRepo.WithDB(tx)
		recycleRepo := s.recycleRepo.WithDB(tx)
		siteRepo := s.siteRepo.WithDB(tx)

		if _, err := bucketRepo.LockByName(ctx, name); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}

			return nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_lookup_failed", "failed to lock bucket", err)
		}

		objects, err := objectRepo.ListAllByBucket(ctx, name)
		if err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_lookup_failed", "failed to load bucket objects", err)
		}
		recycleObjects, err := recycleRepo.ListAllByBucket(ctx, name)
		if err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_lookup_failed", "failed to load recycle bin objects", err)
		}

		storagePaths := make([]string, 0, len(objects)+len(recycleObjects))
		for _, object := range objects {
			if object.StoragePath == "" {
				continue
			}
			storagePaths = append(storagePaths, object.StoragePath)
		}
		for _, object := range recycleObjects {
			if object.StoragePath == "" {
				continue
			}
			storagePaths = append(storagePaths, object.StoragePath)
		}

		if err := siteRepo.DeleteByBucket(ctx, name); err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_delete_failed", "failed to delete bucket sites", err)
		}
		if err := recycleRepo.HardDeleteByBucket(ctx, name); err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_delete_failed", "failed to delete recycle bin objects", err)
		}
		if err := objectRepo.HardDeleteByBucket(ctx, name); err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_delete_failed", "failed to delete bucket objects", err)
		}

		deleted, err := bucketRepo.DeleteByName(ctx, name)
		if err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_delete_failed", "failed to delete bucket", err)
		}
		if !deleted {
			return nil, gorm.ErrRecordNotFound
		}

		return storagePaths, nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
		}
		if appErr := apperrors.From(err); appErr.Code != "internal_error" {
			return err
		}

		return apperrors.Wrap(http.StatusInternalServerError, "bucket_delete_failed", "failed to delete bucket", err)
	}

	return nil
}
