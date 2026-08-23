package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
)

func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				response.Error(c, apperrors.Wrap(
					http.StatusInternalServerError,
					"internal_error",
					"internal server error",
					fmt.Errorf("panic recovered: %v\n%s", recovered, debug.Stack()),
				))
				c.Abort()
			}
		}()

		c.Next()
	}
}
