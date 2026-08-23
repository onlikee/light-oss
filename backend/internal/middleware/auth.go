package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
)

type TokenValidator struct {
	allowedScopes map[string]struct{}
}

const authenticatedBearerScopeKey = "authenticated_bearer_scope"

func NewTokenValidator(tokens []string) *TokenValidator {
	allowedScopes := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		allowedScopes[bearerScope(token)] = struct{}{}
	}

	return &TokenValidator{allowedScopes: allowedScopes}
}

func (v *TokenValidator) RequireBearer() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !v.HasValidBearer(c) {
			response.Error(c, apperrors.New(http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token"))
			c.Abort()
			return
		}

		c.Next()
	}
}

func (v *TokenValidator) HasValidBearer(c *gin.Context) bool {
	scope, ok := v.validatedScope(c.GetHeader("Authorization"))
	if !ok {
		return false
	}

	c.Set(authenticatedBearerScopeKey, scope)
	return true
}

func (v *TokenValidator) HasValidRequest(authorization string) bool {
	_, ok := v.validatedScope(authorization)
	return ok
}

func (v *TokenValidator) validatedScope(authorization string) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(authorization), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	scope := bearerScope(strings.TrimSpace(parts[1]))
	_, ok := v.allowedScopes[scope]
	return scope, ok
}

func AuthenticatedBearerScope(c *gin.Context) (string, bool) {
	scope, ok := c.Get(authenticatedBearerScopeKey)
	if !ok {
		return "", false
	}

	value, ok := scope.(string)
	return value, ok
}

func bearerScope(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
