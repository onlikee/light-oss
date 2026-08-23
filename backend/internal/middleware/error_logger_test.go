package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
)

func TestErrorLoggerRecordsCauseWithoutLeakingItToResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zap.ErrorLevel)
	router := gin.New()
	router.Use(RequestID(), ErrorLogger(zap.New(core)))
	router.GET("/failed", func(c *gin.Context) {
		response.Error(c, apperrors.Wrap(
			http.StatusInternalServerError,
			"store_failed",
			"failed to store object",
			errors.New("disk contains private detail"),
		))
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/failed", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", recorder.Code)
	}
	if body := recorder.Body.String(); strings.Contains(body, "private detail") {
		t.Fatalf("response leaked internal cause: %s", body)
	}
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one error log, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["error_code"] != "store_failed" || fields["path"] != "/failed" {
		t.Fatalf("unexpected structured fields: %+v", fields)
	}
	if fields["error"] != "failed to store object: disk contains private detail" {
		t.Fatalf("expected underlying cause in log, got %+v", fields["error"])
	}
}

func TestRecoveryReportsPanicsThroughStructuredErrorLogger(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zap.ErrorLevel)
	router := gin.New()
	router.Use(RequestID(), ErrorLogger(zap.New(core)), Recovery())
	router.GET("/panic", func(*gin.Context) {
		panic("private panic detail")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", recorder.Code)
	}
	if body := recorder.Body.String(); strings.Contains(body, "private panic detail") {
		t.Fatalf("response leaked panic detail: %s", body)
	}
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one error log, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["error_code"] != "internal_error" || !strings.Contains(fields["error"].(string), "private panic detail") {
		t.Fatalf("unexpected panic log fields: %+v", fields)
	}
}
