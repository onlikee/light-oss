package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/requestid"
)

func ErrorLogger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		for _, ginErr := range c.Errors {
			appErr := apperrors.From(ginErr.Err)
			path := c.FullPath()
			if path == "" {
				path = c.Request.URL.Path
			}
			fields := []zap.Field{
				zap.String("request_id", requestid.Get(c.Request.Context())),
				zap.String("method", c.Request.Method),
				zap.String("path", path),
				zap.Int("status", appErr.Status),
				zap.String("error_code", appErr.Code),
				zap.Error(ginErr.Err),
			}
			if appErr.Status >= http.StatusInternalServerError {
				logger.Error("http_request_failed", fields...)
				continue
			}
			logger.Warn("http_request_rejected", fields...)
		}
	}
}
