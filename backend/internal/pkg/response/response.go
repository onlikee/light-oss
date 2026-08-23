package response

import (
	"github.com/gin-gonic/gin"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/requestid"
)

type envelope struct {
	RequestID string     `json:"request_id"`
	Data      any        `json:"data,omitempty"`
	Error     *errorBody `json:"error,omitempty"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func JSON(c *gin.Context, status int, data any) {
	c.JSON(status, envelope{
		RequestID: requestid.Get(c.Request.Context()),
		Data:      data,
	})
}

func Error(c *gin.Context, err error) {
	appErr := apperrors.From(err)
	_ = c.Error(err)
	c.JSON(appErr.Status, envelope{
		RequestID: requestid.Get(c.Request.Context()),
		Error: &errorBody{
			Code:    appErr.Code,
			Message: appErr.Message,
		},
	})
}

func NoContent(c *gin.Context, status int) {
	c.Status(status)
}
