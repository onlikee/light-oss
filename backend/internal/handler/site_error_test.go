package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"light-oss/backend/internal/middleware"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/requestid"
)

func TestWriteWebsiteErrorRegistersServerErrorForStructuredLogging(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zap.ErrorLevel)
	logger := zap.New(core)
	handler := &apiHandler{logger: logger}
	router := gin.New()
	router.Use(middleware.RequestID(), middleware.ErrorLogger(logger))
	router.GET("/sites/:siteID", func(c *gin.Context) {
		handler.writeWebsiteError(c, apperrors.Wrap(
			http.StatusInternalServerError,
			"site_content_failed",
			"failed to serve site content",
			errors.New("private storage failure"),
		))
	})

	request := httptest.NewRequest(http.MethodGet, "/sites/example", nil)
	request.Header.Set(requestid.HeaderName, "site-request-id")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("response body = %q, want empty", recorder.Body.String())
	}
	if got := recorder.Header().Get(requestid.HeaderName); got != "site-request-id" {
		t.Fatalf("response request ID = %q, want site-request-id", got)
	}

	entries := logs.FilterMessage("http_request_failed").All()
	if len(entries) != 1 {
		t.Fatalf("structured error logs = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	for field, want := range map[string]string{
		"request_id": "site-request-id",
		"method":     http.MethodGet,
		"path":       "/sites/:siteID",
		"error_code": "site_content_failed",
	} {
		if got := fields[field]; got != want {
			t.Errorf("log field %s = %v, want %q", field, got, want)
		}
	}
	if got := fields["error"]; got != "failed to serve site content: private storage failure" {
		t.Errorf("log error = %v, want original cause", got)
	}
}
