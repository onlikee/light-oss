package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
	"light-oss/backend/internal/service"
)

func (h *apiHandler) publishSite(c *gin.Context) {
	reader, err := c.Request.MultipartReader()
	if err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_multipart_request", "multipart form is invalid"))
		return
	}

	tempDir, err := os.MkdirTemp("", "light-oss-site-publish-*")
	if err != nil {
		response.Error(c, apperrors.Wrap(http.StatusInternalServerError, "batch_file_buffer_failed", "failed to buffer uploaded files", err))
		return
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	fileParts, formValues, err := readBatchMultipartRequest(reader, tempDir)
	if err != nil {
		response.Error(c, err)
		return
	}

	manifest, err := parseUploadBatchManifestValue(formValues["manifest"])
	if err != nil {
		response.Error(c, err)
		return
	}

	items, err := buildUploadBatchItemsFromManifest(fileParts, manifest)
	if err != nil {
		response.Error(c, err)
		return
	}

	domains, err := parseSitePublishDomains(formValues["domains"])
	if err != nil {
		response.Error(c, err)
		return
	}

	enabled, err := parseSitePublishBool(formValues["enabled"], true, "enabled")
	if err != nil {
		response.Error(c, err)
		return
	}
	spaFallback, err := parseSitePublishBool(formValues["spa_fallback"], true, "spa_fallback")
	if err != nil {
		response.Error(c, err)
		return
	}

	result, err := h.sitePublishService.Publish(c.Request.Context(), service.PublishSiteUploadInput{
		BucketName:    strings.TrimSpace(formValues["bucket"]),
		ParentPrefix:  strings.TrimSpace(formValues["parent_prefix"]),
		Enabled:       enabled,
		IndexDocument: strings.TrimSpace(formValues["index_document"]),
		ErrorDocument: strings.TrimSpace(formValues["error_document"]),
		SPAFallback:   spaFallback,
		Domains:       domains,
		Items:         items,
	})
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusCreated, sitePublishResponse{
		UploadedCount: result.UploadedCount,
		Site:          siteToResponse(*result.Site),
	})
}

func parseSitePublishDomains(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_request", "domains is required")
	}

	var domains []string
	if err := json.Unmarshal([]byte(raw), &domains); err != nil {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_request", "domains must be a JSON string array")
	}
	if len(domains) == 0 {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_request", "domains is required")
	}

	return domains, nil
}

func parseSitePublishBool(raw string, defaultValue bool, fieldName string) (bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultValue, nil
	}

	switch trimmed {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, apperrors.New(http.StatusBadRequest, "invalid_request", fieldName+" must be true or false")
	}
}
