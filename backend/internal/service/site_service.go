package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
)

const defaultSiteIndexDocument = "index.html"

type SiteInput struct {
	BucketName    string
	RootPrefix    string
	Enabled       bool
	IndexDocument string
	ErrorDocument string
	SPAFallback   bool
	Domains       []string
}

type SiteContent struct {
	Object     *model.Object
	Reader     io.ReadCloser
	StatusCode int
}

type SiteService struct {
	bucketRepo    *repository.BucketRepository
	siteRepo      *repository.SiteRepository
	objectService *ObjectService
}

func NewSiteService(
	bucketRepo *repository.BucketRepository,
	siteRepo *repository.SiteRepository,
	objectService *ObjectService,
) *SiteService {
	return &SiteService{
		bucketRepo:    bucketRepo,
		siteRepo:      siteRepo,
		objectService: objectService,
	}
}

func (s *SiteService) Create(ctx context.Context, input SiteInput) (*model.Site, error) {
	site, domains, err := s.buildSiteInput(ctx, input)
	if err != nil {
		return nil, err
	}

	var created *model.Site
	err = s.siteRepo.Transaction(ctx, func(repo *repository.SiteRepository) error {
		var createErr error
		created, createErr = repo.Create(ctx, site, domains)
		return createErr
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateError(err) {
			return nil, apperrors.New(http.StatusConflict, "domain_conflict", "domain is already bound to another site")
		}
		if isForeignKeyError(err) {
			return nil, apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
		}

		return nil, apperrors.Wrap(http.StatusInternalServerError, "site_create_failed", "failed to create site", err)
	}

	return created, nil
}

func (s *SiteService) List(ctx context.Context) ([]model.Site, error) {
	sites, err := s.siteRepo.List(ctx)
	if err != nil {
		return nil, apperrors.Wrap(http.StatusInternalServerError, "site_list_failed", "failed to list sites", err)
	}

	return sites, nil
}

func (s *SiteService) Get(ctx context.Context, id uint64) (*model.Site, error) {
	site, err := s.siteRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.New(http.StatusNotFound, "site_not_found", "site not found")
		}

		return nil, apperrors.Wrap(http.StatusInternalServerError, "site_lookup_failed", "failed to look up site", err)
	}

	return site, nil
}

func (s *SiteService) Update(ctx context.Context, id uint64, input SiteInput) (*model.Site, error) {
	site, domains, err := s.buildSiteInput(ctx, input)
	if err != nil {
		return nil, err
	}
	site.ID = id

	var updated *model.Site
	err = s.siteRepo.Transaction(ctx, func(repo *repository.SiteRepository) error {
		var updateErr error
		updated, updateErr = repo.Update(ctx, site, domains)
		return updateErr
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.New(http.StatusNotFound, "site_not_found", "site not found")
		}
		if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateError(err) {
			return nil, apperrors.New(http.StatusConflict, "domain_conflict", "domain is already bound to another site")
		}
		if isForeignKeyError(err) {
			return nil, apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
		}

		return nil, apperrors.Wrap(http.StatusInternalServerError, "site_update_failed", "failed to update site", err)
	}

	return updated, nil
}

func (s *SiteService) Delete(ctx context.Context, id uint64) error {
	err := s.siteRepo.Transaction(ctx, func(repo *repository.SiteRepository) error {
		deleted, deleteErr := repo.Delete(ctx, id)
		if deleteErr != nil {
			return deleteErr
		}
		if !deleted {
			return gorm.ErrRecordNotFound
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.New(http.StatusNotFound, "site_not_found", "site not found")
		}

		return apperrors.Wrap(http.StatusInternalServerError, "site_delete_failed", "failed to delete site", err)
	}

	return nil
}

func (s *SiteService) FindByDomain(ctx context.Context, host string) (*model.Site, error) {
	normalized := NormalizeRequestHost(host)
	if normalized == "" {
		return nil, nil
	}

	site, err := s.siteRepo.FindByDomain(ctx, normalized)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}

		return nil, apperrors.Wrap(http.StatusInternalServerError, "site_lookup_failed", "failed to look up site", err)
	}

	return site, nil
}

func (s *SiteService) OpenContent(ctx context.Context, site *model.Site, requestPath string) (*SiteContent, error) {
	if site == nil || !site.Enabled {
		return nil, apperrors.New(http.StatusNotFound, "website_not_found", "website not found")
	}

	resolvedPath, directoryHint, err := normalizeWebsiteRequestPath(requestPath)
	if err != nil {
		return nil, apperrors.New(http.StatusNotFound, "website_not_found", "website not found")
	}

	candidates := siteCandidateKeys(site, resolvedPath, directoryHint)
	for _, candidate := range candidates {
		content, openErr := s.openPublicObject(ctx, site.BucketName, candidate.key)
		if openErr != nil {
			return nil, openErr
		}
		if content != nil {
			content.StatusCode = candidate.statusCode
			return content, nil
		}
	}

	return nil, apperrors.New(http.StatusNotFound, "website_not_found", "website not found")
}

func ParseSiteID(raw string) (uint64, error) {
	id, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || id == 0 {
		return 0, apperrors.New(http.StatusBadRequest, "invalid_site_id", "site id is invalid")
	}

	return id, nil
}

func (s *SiteService) buildSiteInput(ctx context.Context, input SiteInput) (*model.Site, []string, error) {
	if err := ValidateBucketName(input.BucketName); err != nil {
		return nil, nil, err
	}

	exists, err := s.bucketRepo.Exists(ctx, input.BucketName)
	if err != nil {
		return nil, nil, apperrors.Wrap(http.StatusInternalServerError, "bucket_lookup_failed", "failed to look up bucket", err)
	}
	if !exists {
		return nil, nil, apperrors.New(http.StatusNotFound, "bucket_not_found", "bucket not found")
	}

	rootPrefix, err := NormalizeSiteRootPrefix(input.RootPrefix)
	if err != nil {
		return nil, nil, err
	}
	indexDocument, err := NormalizeSiteDocument(input.IndexDocument, defaultSiteIndexDocument, false)
	if err != nil {
		return nil, nil, err
	}
	errorDocument, err := NormalizeSiteDocument(input.ErrorDocument, "", true)
	if err != nil {
		return nil, nil, err
	}
	domains, err := NormalizeSiteDomains(input.Domains)
	if err != nil {
		return nil, nil, err
	}

	return &model.Site{
		BucketName:    input.BucketName,
		RootPrefix:    rootPrefix,
		Enabled:       input.Enabled,
		IndexDocument: indexDocument,
		ErrorDocument: errorDocument,
		SPAFallback:   input.SPAFallback,
	}, domains, nil
}

type siteCandidate struct {
	key        string
	statusCode int
}

func siteCandidateKeys(site *model.Site, resolvedPath string, directoryHint bool) []siteCandidate {
	candidates := make([]siteCandidate, 0, 4)
	indexKey := siteObjectKey(site.RootPrefix, site.IndexDocument)

	if resolvedPath == "" {
		return []siteCandidate{{key: indexKey, statusCode: http.StatusOK}}
	}

	if directoryHint {
		candidates = append(candidates, siteCandidate{
			key:        siteObjectKey(site.RootPrefix, resolvedPath+"/"+site.IndexDocument),
			statusCode: http.StatusOK,
		})
	} else {
		candidates = append(candidates, siteCandidate{
			key:        siteObjectKey(site.RootPrefix, resolvedPath),
			statusCode: http.StatusOK,
		})
		candidates = append(candidates, siteCandidate{
			key:        siteObjectKey(site.RootPrefix, resolvedPath+"/"+site.IndexDocument),
			statusCode: http.StatusOK,
		})
	}

	if site.SPAFallback {
		candidates = append(candidates, siteCandidate{
			key:        indexKey,
			statusCode: http.StatusOK,
		})
	} else if site.ErrorDocument != "" {
		candidates = append(candidates, siteCandidate{
			key:        siteObjectKey(site.RootPrefix, site.ErrorDocument),
			statusCode: http.StatusNotFound,
		})
	}

	return candidates
}

func siteObjectKey(rootPrefix string, value string) string {
	if rootPrefix == "" {
		return value
	}

	return rootPrefix + value
}

func normalizeWebsiteRequestPath(raw string) (string, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "/" {
		return "", true, nil
	}
	if strings.Contains(trimmed, "\x00") || strings.Contains(trimmed, "\\") {
		return "", false, errors.New("invalid path")
	}

	directoryHint := strings.HasSuffix(trimmed, "/")
	segments := strings.Split(strings.Trim(trimmed, "/"), "/")
	cleaned := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		if segment == "." || segment == ".." {
			return "", false, errors.New("invalid path")
		}
		cleaned = append(cleaned, segment)
	}

	if len(cleaned) == 0 {
		return "", true, nil
	}

	return strings.Join(cleaned, "/"), directoryHint, nil
}

func (s *SiteService) openPublicObject(ctx context.Context, bucketName string, objectKey string) (*SiteContent, error) {
	object, reader, err := s.objectService.Open(ctx, bucketName, objectKey)
	if err != nil {
		appErr := apperrors.From(err)
		if appErr.Status == http.StatusNotFound {
			return nil, nil
		}

		return nil, err
	}
	if object.Visibility != model.VisibilityPublic {
		_ = reader.Close()
		return nil, nil
	}

	return &SiteContent{
		Object: object,
		Reader: reader,
	}, nil
}
