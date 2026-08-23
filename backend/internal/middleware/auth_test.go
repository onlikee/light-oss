package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequireBearerStoresValidatedHashedScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	validator := NewTokenValidator([]string{"test-token"})
	router := gin.New()
	router.Use(validator.RequireBearer())

	var scope string
	router.GET("/protected", func(c *gin.Context) {
		var ok bool
		scope, ok = AuthenticatedBearerScope(c)
		if !ok {
			t.Fatal("expected authenticated bearer scope")
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if scope != bearerScope("test-token") {
		t.Fatalf("unexpected bearer scope %q", scope)
	}
	if scope == "test-token" {
		t.Fatal("authenticated scope must not expose the bearer token")
	}
}

func TestHasValidBearerDoesNotStoreScopeForInvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	validator := NewTokenValidator([]string{"test-token"})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Authorization", "Bearer invalid-token")

	if validator.HasValidBearer(c) {
		t.Fatal("expected invalid token to be rejected")
	}
	if _, ok := AuthenticatedBearerScope(c); ok {
		t.Fatal("invalid token must not create an authenticated scope")
	}
}
