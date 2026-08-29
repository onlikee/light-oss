package handler

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
	"light-oss/backend/internal/service"
)

func (h *apiHandler) publishSiteFile(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		if isRequestBodyTooLarge(err) {
			response.Error(c, apperrors.New(http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds configured upload size"))
			return
		}
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "file is required"))
		return
	}

	domains, err := parseSitePublishDomains(c.PostForm("domains"))
	if err != nil {
		response.Error(c, err)
		return
	}

	enabled, err := parseSitePublishBool(c.PostForm("enabled"), true, "enabled")
	if err != nil {
		response.Error(c, err)
		return
	}
	spaFallback, err := parseSitePublishBool(c.PostForm("spa_fallback"), true, "spa_fallback")
	if err != nil {
		response.Error(c, err)
		return
	}

	site, err := h.sitePublishService.PublishFile(c.Request.Context(), service.PublishSiteFileInput{
		BucketName:    strings.TrimSpace(c.PostForm("bucket")),
		ParentPrefix:  strings.TrimSpace(c.PostForm("parent_prefix")),
		Enabled:       enabled,
		ErrorDocument: strings.TrimSpace(c.PostForm("error_document")),
		SPAFallback:   spaFallback,
		Domains:       domains,
		Filename:      fileHeader.Filename,
		ContentType:   fileHeader.Header.Get("Content-Type"),
		Open: func() (io.ReadCloser, error) {
			return fileHeader.Open()
		},
	})
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusCreated, siteToResponse(*site))
}
