package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
)

type PublishSiteUploadInput struct {
	BucketName    string
	ParentPrefix  string
	Enabled       bool
	IndexDocument string
	ErrorDocument string
	SPAFallback   bool
	Domains       []string
	Items         []UploadObjectBatchItemInput
}

type PublishSiteUploadOutput struct {
	UploadedCount int
	Site          *model.Site
}

type SitePublishService struct {
	gormDB      *gorm.DB
	objectRepo  *repository.ObjectRepository
	siteRepo    *repository.SiteRepository
	siteService *SiteService
	blobs       *BlobLifecycleService
}

func NewSitePublishService(
	gormDB *gorm.DB,
	objectRepo *repository.ObjectRepository,
	siteRepo *repository.SiteRepository,
	blobLifecycle *BlobLifecycleService,
	siteService *SiteService,
) *SitePublishService {
	return &SitePublishService{
		gormDB:      gormDB,
		objectRepo:  objectRepo,
		siteRepo:    siteRepo,
		blobs:       blobLifecycle,
		siteService: siteService,
	}
}

func (s *SitePublishService) Publish(
	ctx context.Context,
	input PublishSiteUploadInput,
) (*PublishSiteUploadOutput, error) {
	if len(input.Items) == 0 {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest must contain at least one file")
	}
	if len(input.Items) > maxBatchUploadItems {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest must contain at most 2000 files")
	}
	if len(input.Domains) == 0 {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_request", "domains is required")
	}

	parentPrefix, err := normalizePublishParentPrefix(input.ParentPrefix)
	if err != nil {
		return nil, err
	}

	topLevelFolder, err := sharedTopLevelFolderName(input.Items)
	if err != nil {
		return nil, err
	}

	site, domains, err := s.siteService.buildSiteInput(ctx, SiteInput{
		BucketName:    input.BucketName,
		RootPrefix:    parentPrefix + topLevelFolder + "/",
		Enabled:       input.Enabled,
		IndexDocument: input.IndexDocument,
		ErrorDocument: input.ErrorDocument,
		SPAFallback:   input.SPAFallback,
		Domains:       input.Domains,
	})
	if err != nil {
		return nil, err
	}

	preparedItems := make([]preparedBatchUploadItem, 0, len(input.Items))
	objectKeys := make([]string, 0, len(input.Items))
	seenObjectKeys := make(map[string]struct{}, len(input.Items))
	for _, item := range input.Items {
		if err := ValidateUploadRelativePath(item.RelativePath); err != nil {
			return nil, invalidBatchManifestError(err)
		}
		objectKey := parentPrefix + item.RelativePath
		if err := ValidateUserObjectKey(objectKey); err != nil {
			return nil, invalidBatchManifestError(err)
		}
		if _, exists := seenObjectKeys[objectKey]; exists {
			return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest contains duplicate object keys")
		}
		seenObjectKeys[objectKey] = struct{}{}
		objectKeys = append(objectKeys, objectKey)
		preparedItems = append(preparedItems, preparedBatchUploadItem{Item: item, ObjectKey: objectKey})
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
			Visibility:       model.VisibilityPublic,
		})
	}

	var createdSite *model.Site
	err = s.blobs.Publish(ctx, stagedBlobs, func(tx *gorm.DB) ([]string, error) {
		objectRepo := s.objectRepo.WithDB(tx)
		siteRepo := s.siteRepo.WithDB(tx)
		existingObjects, err := findActiveObjectsInBatches(ctx, objectRepo, input.BucketName, objectKeys, true)
		if err != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "object_lookup_failed", "failed to look up objects", err)
		}
		if err := objectRepo.UpsertBatch(ctx, objectsToSave, metadataWriteBatchSize); err != nil {
			if isForeignKeyError(err) {
				return nil, apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
			}
			return nil, apperrors.Wrap(http.StatusInternalServerError, "object_metadata_failed", "failed to save object metadata", err)
		}

		createdSite, err = siteRepo.Create(ctx, site, domains)
		if err != nil {
			return nil, err
		}
		return storagePathsFromObjects(existingObjects), nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateError(err) {
			return nil, apperrors.New(http.StatusConflict, "domain_conflict", "domain is already bound to another site")
		}
		if isForeignKeyError(err) {
			return nil, apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
		}

		if appErr := apperrors.From(err); appErr.Code != "internal_error" {
			return nil, err
		}

		return nil, apperrors.Wrap(http.StatusInternalServerError, "site_create_failed", "failed to create site", err)
	}

	return &PublishSiteUploadOutput{
		UploadedCount: len(objectsToSave),
		Site:          createdSite,
	}, nil
}

func sharedTopLevelFolderName(items []UploadObjectBatchItemInput) (string, error) {
	topLevelFolder := ""

	for _, item := range items {
		if err := ValidateUploadRelativePath(item.RelativePath); err != nil {
			return "", invalidBatchManifestError(err)
		}

		segments := strings.Split(item.RelativePath, "/")
		if len(segments) < 2 {
			return "", apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest entry must include a top-level folder")
		}

		currentTopLevel := strings.TrimSpace(segments[0])
		if currentTopLevel == "" {
			return "", apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest entry is invalid")
		}

		if topLevelFolder == "" {
			topLevelFolder = currentTopLevel
			continue
		}
		if currentTopLevel != topLevelFolder {
			return "", apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest entries must share the same top-level folder")
		}
	}

	return topLevelFolder, nil
}

func normalizePublishParentPrefix(value string) (string, error) {
	normalized := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	normalized = strings.TrimPrefix(normalized, "/")
	if normalized == "" {
		return "", nil
	}
	if !strings.HasSuffix(normalized, "/") {
		normalized += "/"
	}
	if err := ValidateFolderPrefix(normalized); err != nil {
		return "", err
	}

	return normalized, nil
}
