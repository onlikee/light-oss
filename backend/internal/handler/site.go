package handler

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
	"light-oss/backend/internal/service"
)

type siteRequest struct {
	Bucket        string   `json:"bucket"`
	RootPrefix    string   `json:"root_prefix"`
	Enabled       *bool    `json:"enabled"`
	IndexDocument string   `json:"index_document"`
	ErrorDocument string   `json:"error_document"`
	SPAFallback   *bool    `json:"spa_fallback"`
	Domains       []string `json:"domains"`
}

type siteResponse struct {
	ID            uint64    `json:"id"`
	Bucket        string    `json:"bucket"`
	RootPrefix    string    `json:"root_prefix"`
	Enabled       bool      `json:"enabled"`
	IndexDocument string    `json:"index_document"`
	ErrorDocument string    `json:"error_document"`
	SPAFallback   bool      `json:"spa_fallback"`
	Domains       []string  `json:"domains"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type sitePublishResponse struct {
	UploadedCount int          `json:"uploaded_count"`
	Site          siteResponse `json:"site"`
}

func (h *apiHandler) createSite(c *gin.Context) {
	var req siteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}

	site, err := h.siteService.Create(c.Request.Context(), siteInputFromRequest(req))
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusCreated, siteToResponse(*site))
}

func (h *apiHandler) listSites(c *gin.Context) {
	sites, err := h.siteService.List(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}

	items := make([]siteResponse, 0, len(sites))
	for _, site := range sites {
		items = append(items, siteToResponse(site))
	}

	response.JSON(c, http.StatusOK, gin.H{"items": items})
}

func (h *apiHandler) getSite(c *gin.Context) {
	siteID, err := service.ParseSiteID(c.Param("siteID"))
	if err != nil {
		response.Error(c, err)
		return
	}

	site, err := h.siteService.Get(c.Request.Context(), siteID)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, siteToResponse(*site))
}

func (h *apiHandler) updateSite(c *gin.Context) {
	siteID, err := service.ParseSiteID(c.Param("siteID"))
	if err != nil {
		response.Error(c, err)
		return
	}

	var req siteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}

	site, err := h.siteService.Update(c.Request.Context(), siteID, siteInputFromRequest(req))
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, siteToResponse(*site))
}

func (h *apiHandler) deleteSite(c *gin.Context) {
	siteID, err := service.ParseSiteID(c.Param("siteID"))
	if err != nil {
		response.Error(c, err)
		return
	}

	if err := h.siteService.Delete(c.Request.Context(), siteID); err != nil {
		response.Error(c, err)
		return
	}

	response.NoContent(c, http.StatusNoContent)
}

func (h *apiHandler) headSite(c *gin.Context) {
	h.serveSiteByID(c, true)
}

func (h *apiHandler) downloadSite(c *gin.Context) {
	h.serveSiteByID(c, false)
}

func (h *apiHandler) serveSiteByID(c *gin.Context, headOnly bool) {
	siteID, err := service.ParseSiteID(c.Param("siteID"))
	if err != nil {
		status := apperrors.From(err).Status
		c.Status(status)
		return
	}

	site, err := h.siteService.Get(c.Request.Context(), siteID)
	if err != nil {
		h.writeWebsiteError(c, err)
		return
	}

	h.serveSiteContent(c, site, c.Param("path"), headOnly)
}

func (h *apiHandler) noRoute(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Status(http.StatusNotFound)
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/api/") {
		c.Status(http.StatusNotFound)
		return
	}

	site, err := h.siteService.FindByDomain(c.Request.Context(), c.Request.Host)
	if err != nil {
		h.writeWebsiteError(c, err)
		return
	}
	if site == nil {
		c.Status(http.StatusNotFound)
		return
	}

	h.serveSiteContent(c, site, c.Request.URL.Path, c.Request.Method == http.MethodHead)
}

func (h *apiHandler) serveSiteContent(c *gin.Context, site *model.Site, requestPath string, headOnly bool) {
	content, err := h.siteService.OpenContent(c.Request.Context(), site, requestPath)
	if err != nil {
		h.writeWebsiteError(c, err)
		return
	}
	defer func() {
		if content.Reader != nil {
			_ = content.Reader.Close()
		}
	}()

	setObjectHeaders(c, content.Object, false)
	c.Status(content.StatusCode)
	if headOnly {
		return
	}

	if _, err := io.Copy(c.Writer, content.Reader); err != nil {
		h.logger.Error("stream site content", zap.Error(err))
	}
}

func (h *apiHandler) writeWebsiteError(c *gin.Context, err error) {
	appErr := apperrors.From(err)
	if appErr.Status >= http.StatusInternalServerError {
		_ = c.Error(err)
	}
	c.Status(appErr.Status)
}

func siteToResponse(site model.Site) siteResponse {
	domains := make([]string, 0, len(site.Domains))
	for _, domain := range site.Domains {
		domains = append(domains, domain.Domain)
	}

	return siteResponse{
		ID:            site.ID,
		Bucket:        site.BucketName,
		RootPrefix:    site.RootPrefix,
		Enabled:       site.Enabled,
		IndexDocument: site.IndexDocument,
		ErrorDocument: site.ErrorDocument,
		SPAFallback:   site.SPAFallback,
		Domains:       domains,
		CreatedAt:     site.CreatedAt,
		UpdatedAt:     site.UpdatedAt,
	}
}

func siteInputFromRequest(req siteRequest) service.SiteInput {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	spaFallback := false
	if req.SPAFallback != nil {
		spaFallback = *req.SPAFallback
	}

	return service.SiteInput{
		BucketName:    req.Bucket,
		RootPrefix:    req.RootPrefix,
		Enabled:       enabled,
		IndexDocument: req.IndexDocument,
		ErrorDocument: req.ErrorDocument,
		SPAFallback:   spaFallback,
		Domains:       req.Domains,
	}
}
