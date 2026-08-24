package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
)

const (
	maxBatchUploadItems    = 2000
	metadataWriteBatchSize = 100
)

type UploadObjectBatchItemInput struct {
	RelativePath     string
	OriginalFilename string
	ContentType      string
	Size             *uint64
	Open             func() (io.ReadCloser, error)
}

type UploadObjectBatchInput struct {
	BucketName     string
	Prefix         string
	Visibility     string
	AllowOverwrite bool
	Items          []UploadObjectBatchItemInput
}

type UploadObjectBatchOutput struct {
	UploadedCount int
	Items         []model.Object
}

func (s *ObjectService) UploadBatch(
	ctx context.Context,
	input UploadObjectBatchInput,
) (*UploadObjectBatchOutput, error) {
	if err := ValidateBucketName(input.BucketName); err != nil {
		return nil, err
	}
	if err := ValidateFolderPrefix(input.Prefix); err != nil {
		return nil, err
	}
	if len(input.Items) == 0 {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest must contain at least one file")
	}
	if len(input.Items) > maxBatchUploadItems {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest must contain at most 2000 files")
	}

	visibility, err := ParseVisibility(input.Visibility)
	if err != nil {
		return nil, err
	}

	exists, err := s.bucketRepo.Exists(ctx, input.BucketName)
	if err != nil {
		return nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_lookup_failed", "failed to look up bucket", err)
	}
	if !exists {
		return nil, apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
	}

	preparedItems := make([]preparedBatchUploadItem, 0, len(input.Items))
	seenObjectKeys := make(map[string]struct{}, len(input.Items))
	for _, item := range input.Items {
		if err := ValidateUploadRelativePath(item.RelativePath); err != nil {
			return nil, invalidBatchManifestError(err)
		}

		objectKey := input.Prefix + item.RelativePath
		if err := ValidateUserObjectKey(objectKey); err != nil {
			return nil, invalidBatchManifestError(err)
		}
		if _, exists := seenObjectKeys[objectKey]; exists {
			return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest contains duplicate object keys")
		}
		seenObjectKeys[objectKey] = struct{}{}

		preparedItems = append(preparedItems, preparedBatchUploadItem{
			Item:      item,
			ObjectKey: objectKey,
		})
	}

	if !input.AllowOverwrite {
		existingKeys, err := s.objectRepo.ListExistingActiveKeys(ctx, input.BucketName, collectPreparedObjectKeys(preparedItems))
		if err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "object_lookup_failed", "failed to look up objects", err)
		}
		if len(existingKeys) > 0 {
			return nil, apperrors.New(http.StatusConflict, "object_exists", "one or more objects already exist; set X-Allow-Overwrite=true to overwrite")
		}
	}

	stagedBlobs, err := stageBatchUploadItems(ctx, s.blobs, preparedItems)
	if err != nil {
		return nil, err
	}

	objectsToSave := make([]model.Object, 0, len(preparedItems))
	for index, prepared := range preparedItems {
		staged := stagedBlobs[index]
		objectsToSave = append(objectsToSave, model.Object{
			BucketName:       input.BucketName,
			ObjectKey:        prepared.ObjectKey,
			OriginalFilename: SanitizeOriginalFilename(prepared.Item.OriginalFilename),
			StoragePath:      staged.StoragePath,
			Size:             int64(staged.Size),
			ContentType:      NormalizeContentType(prepared.Item.ContentType),
			ETag:             staged.ETag,
			Visibility:       visibility,
		})
	}

	objectKeys := collectPreparedObjectKeys(preparedItems)
	uploadedItems := make([]model.Object, 0, len(objectsToSave))
	err = s.blobs.Publish(ctx, stagedBlobs, func(tx *gorm.DB) ([]string, error) {
		repo := s.objectRepo.WithDB(tx)
		existingObjects, err := findActiveObjectsInBatches(ctx, repo, input.BucketName, objectKeys, true)
		if err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "object_lookup_failed", "failed to look up objects", err)
		}
		if !input.AllowOverwrite && len(existingObjects) > 0 {
			return nil, apperrors.New(http.StatusConflict, "object_exists", "one or more objects already exist; set X-Allow-Overwrite=true to overwrite")
		}

		if input.AllowOverwrite {
			err = repo.UpsertBatch(ctx, objectsToSave, metadataWriteBatchSize)
		} else {
			err = repo.CreateBatch(ctx, objectsToSave, metadataWriteBatchSize)
		}
		if err != nil {
			if !input.AllowOverwrite && (errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateError(err)) {
				return nil, apperrors.New(http.StatusConflict, "object_exists", "one or more objects already exist; set X-Allow-Overwrite=true to overwrite")
			}
			if isForeignKeyError(err) {
				return nil, apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
			}
			return nil, apperrors.Wrap(http.StatusInternalServerError, "object_metadata_failed", "failed to save object metadata", err)
		}

		uploadedItems, err = findActiveObjectsInBatches(ctx, repo, input.BucketName, objectKeys, false)
		if err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "object_lookup_failed", "failed to load uploaded objects", err)
		}
		uploadedItems = orderObjectsByKeys(uploadedItems, objectKeys)
		if len(uploadedItems) != len(objectKeys) {
			return nil, apperrors.New(http.StatusInternalServerError, "object_metadata_failed", "failed to load all uploaded objects")
		}
		return storagePathsFromObjects(existingObjects), nil
	})
	if err != nil {
		return nil, err
	}

	return &UploadObjectBatchOutput{
		UploadedCount: len(uploadedItems),
		Items:         uploadedItems,
	}, nil
}

func stageBatchUploadItems(
	ctx context.Context,
	blobs *BlobLifecycleService,
	items []preparedBatchUploadItem,
) ([]*StagedBlob, error) {
	knownInputs := make([]BlobBatchInput, 0, len(items))
	allSizesKnown := true
	for _, item := range items {
		if item.Item.Size == nil {
			allSizesKnown = false
			break
		}
		knownInputs = append(knownInputs, BlobBatchInput{
			Size: *item.Item.Size,
			Open: item.Item.Open,
		})
	}
	if allSizesKnown {
		staged, err := blobs.StageKnownBatch(ctx, knownInputs)
		if err != nil {
			var readerErr *batchBlobReaderError
			if errors.As(err, &readerErr) {
				return nil, apperrors.Wrap(http.StatusInternalServerError, "batch_file_open_failed", "failed to "+readerErr.operation+" uploaded file", readerErr.err)
			}
			return nil, stagedBlobStoreError(err)
		}
		return staged, nil
	}

	stagedBlobs := make([]*StagedBlob, 0, len(items))
	for _, item := range items {
		reader, openErr := item.Item.Open()
		if openErr != nil {
			blobs.Abort(ctx, stagedBlobs...)
			return nil, apperrors.Wrap(http.StatusInternalServerError, "batch_file_open_failed", "failed to open uploaded file", openErr)
		}

		staged, stageErr := blobs.Stage(ctx, reader)
		closeErr := reader.Close()
		if stageErr != nil {
			blobs.Abort(ctx, stagedBlobs...)
			return nil, stagedBlobStoreError(stageErr)
		}
		if closeErr != nil {
			blobs.Abort(ctx, append(stagedBlobs, staged)...)
			return nil, apperrors.Wrap(http.StatusInternalServerError, "batch_file_open_failed", "failed to close uploaded file", closeErr)
		}

		stagedBlobs = append(stagedBlobs, staged)
	}

	return stagedBlobs, nil
}

func findActiveObjectsInBatches(
	ctx context.Context,
	repo *repository.ObjectRepository,
	bucketName string,
	objectKeys []string,
	forUpdate bool,
) ([]model.Object, error) {
	objects := make([]model.Object, 0, len(objectKeys))
	for start := 0; start < len(objectKeys); start += metadataWriteBatchSize {
		end := min(start+metadataWriteBatchSize, len(objectKeys))
		var batch []model.Object
		var err error
		if forUpdate {
			batch, err = repo.FindActiveByKeysForUpdate(ctx, bucketName, objectKeys[start:end])
		} else {
			batch, err = repo.FindActiveByKeys(ctx, bucketName, objectKeys[start:end])
		}
		if err != nil {
			return nil, err
		}
		objects = append(objects, batch...)
	}
	return objects, nil
}

func orderObjectsByKeys(objects []model.Object, keys []string) []model.Object {
	byKey := make(map[string]model.Object, len(objects))
	for _, object := range objects {
		byKey[object.ObjectKey] = object
	}

	ordered := make([]model.Object, 0, len(keys))
	for _, key := range keys {
		if object, exists := byKey[key]; exists {
			ordered = append(ordered, object)
		}
	}
	return ordered
}

type preparedBatchUploadItem struct {
	Item      UploadObjectBatchItemInput
	ObjectKey string
}

func collectPreparedObjectKeys(items []preparedBatchUploadItem) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.ObjectKey)
	}

	return keys
}

func invalidBatchManifestError(err error) error {
	appErr := apperrors.From(err)
	message := strings.TrimSpace(appErr.Message)
	if message == "" {
		message = "manifest entry is invalid"
	}

	return apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", message)
}
