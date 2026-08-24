package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
)

type PublishSiteFileInput struct {
	BucketName    string
	ParentPrefix  string
	Enabled       bool
	ErrorDocument string
	SPAFallback   bool
	Domains       []string
	Filename      string
	ContentType   string
	Open          func() (io.ReadCloser, error)
}

func (s *SitePublishService) PublishFile(
	ctx context.Context,
	input PublishSiteFileInput,
) (*model.Site, error) {
	if len(input.Domains) == 0 {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_request", "domains is required")
	}
	if input.Open == nil {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_request", "file is required")
	}

	parentPrefix, err := normalizePublishParentPrefix(input.ParentPrefix)
	if err != nil {
		return nil, err
	}

	fileName, err := normalizePublishFileName(input.Filename)
	if err != nil {
		return nil, err
	}

	site, domains, err := s.siteService.buildSiteInput(ctx, SiteInput{
		BucketName:    input.BucketName,
		RootPrefix:    parentPrefix,
		Enabled:       input.Enabled,
		IndexDocument: fileName,
		ErrorDocument: input.ErrorDocument,
		SPAFallback:   input.SPAFallback,
		Domains:       input.Domains,
	})
	if err != nil {
		return nil, err
	}

	reader, err := input.Open()
	if err != nil {
		return nil, apperrors.Wrap(http.StatusInternalServerError, "batch_file_open_failed", "failed to open uploaded file", err)
	}
	defer func() {
		_ = reader.Close()
	}()

	staged, err := s.blobs.Stage(ctx, reader)
	if err != nil {
		return nil, stagedBlobStoreError(err)
	}

	object := &model.Object{
		BucketName:       input.BucketName,
		ObjectKey:        parentPrefix + fileName,
		OriginalFilename: SanitizeOriginalFilename(input.Filename),
		StoragePath:      staged.StoragePath,
		Size:             int64(staged.Size),
		ContentType:      NormalizeContentType(input.ContentType),
		ETag:             staged.ETag,
		Visibility:       model.VisibilityPublic,
	}

	var createdSite *model.Site
	err = s.blobs.Publish(ctx, []*StagedBlob{staged}, func(tx *gorm.DB) ([]string, error) {
		objectRepo := s.objectRepo.WithDB(tx)
		siteRepo := s.siteRepo.WithDB(tx)
		releasedPaths := make([]string, 0, 1)
		existingObject, findErr := objectRepo.FindActiveForUpdate(ctx, input.BucketName, object.ObjectKey)
		if findErr == nil {
			releasedPaths = append(releasedPaths, existingObject.StoragePath)
		} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "object_lookup_failed", "failed to look up object", findErr)
		}

		if _, err := objectRepo.Upsert(ctx, object); err != nil {
			if isForeignKeyError(err) {
				return nil, apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
			}

			return nil, apperrors.Wrap(http.StatusInternalServerError, "object_metadata_failed", "failed to save object metadata", err)
		}

		createdSite, err = siteRepo.Create(ctx, site, domains)
		if err != nil {
			return nil, err
		}

		return releasedPaths, nil
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

	return createdSite, nil
}

func normalizePublishFileName(value string) (string, error) {
	normalized := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	baseName := strings.TrimSpace(path.Base(normalized))
	if baseName == "" || baseName == "." || baseName == "/" {
		return "", apperrors.New(http.StatusBadRequest, "invalid_request", "file name is invalid")
	}
	if err := ValidateUserObjectKey(baseName); err != nil {
		return "", apperrors.New(http.StatusBadRequest, "invalid_request", "file name is invalid")
	}

	return baseName, nil
}
