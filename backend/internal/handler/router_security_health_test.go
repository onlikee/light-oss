package handler_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"light-oss/backend/internal/config"
)

func TestProtectedRoutesRequireAuth(t *testing.T) {
	router := newTestRouter(t, 1024)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/buckets", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestCORSAllowsAllOrigins(t *testing.T) {
	router := newTestRouter(t, 1024)

	getReq := httptest.NewRequest(http.MethodGet, "/livez", nil)
	getReq.Header.Set("Origin", "http://console.example.com")
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRec.Code)
	}
	if got := getRec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard allow origin on GET, got %q", got)
	}

	optionsReq := httptest.NewRequest(http.MethodOptions, "/api/v1/buckets", nil)
	optionsReq.Header.Set("Origin", "http://console.example.com")
	optionsReq.Header.Set("Access-Control-Request-Method", http.MethodGet)
	optionsReq.Header.Set("Access-Control-Request-Headers", "Authorization")
	optionsRec := httptest.NewRecorder()
	router.ServeHTTP(optionsRec, optionsReq)

	if optionsRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", optionsRec.Code)
	}
	if got := optionsRec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard allow origin on preflight, got %q", got)
	}
	if got := optionsRec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Fatalf("expected Authorization in allow headers, got %q", got)
	}
}

func TestRemovedPublicHealthzReturnsNotFound(t *testing.T) {
	router := newTestRouter(t, 1024)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected removed public health alias to return 404, got %d", rec.Code)
	}
}

func TestCORSHeadersRemainVisibleOnGlobalRateLimit(t *testing.T) {
	router := newTestRouterWithConfig(t, 1024, func(cfg *config.Config) {
		cfg.RateLimitIPRPS = 0.000001
		cfg.RateLimitIPBurst = 1
	})

	firstReq := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	firstReq.Header.Set("Origin", "http://console.example.com")
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusNotFound {
		t.Fatalf("expected first request to reach the not-found handler, got %d", firstRec.Code)
	}

	limitedReq := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	limitedReq.Header.Set("Origin", "http://console.example.com")
	limitedRec := httptest.NewRecorder()
	router.ServeHTTP(limitedRec, limitedReq)
	if limitedRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", limitedRec.Code)
	}
	if got := limitedRec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard allow origin on rate limit response, got %q", got)
	}

	var body apiEnvelope[any]
	decodeJSON(t, limitedRec.Body.Bytes(), &body)
	if body.Error == nil || body.Error.Code != "rate_limited" {
		t.Fatalf("expected rate_limited error, got %+v", body.Error)
	}
}

func TestListBucketsSupportsSearch(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "alpha")
	createBucket(t, router, "beta")

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets?search="+url.QueryEscape("alp"),
		nil,
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[bucketListResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if len(body.Data.Items) != 1 || body.Data.Items[0].Name != "alpha" {
		t.Fatalf("expected only alpha bucket, got %+v", body.Data.Items)
	}
}

func TestListBucketsTreatsSearchWildcardsAsLiterals(t *testing.T) {
	router := newTestRouter(t, 1024)

	createBucket(t, router, "alpha")
	createBucket(t, router, "beta")

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/buckets?search="+url.QueryEscape("%"),
		nil,
	)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[bucketListResponse]
	decodeJSON(t, rec.Body.Bytes(), &body)
	if len(body.Data.Items) != 0 {
		t.Fatalf("expected wildcard search to return no buckets, got %+v", body.Data.Items)
	}
}

func TestProtectedHealthzRequiresAuthAndReturnsHealthState(t *testing.T) {
	router := newTestRouter(t, 1024)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
	unauthorizedRec := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedRec, unauthorizedReq)
	if unauthorizedRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorizedRec.Code)
	}

	authorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
	authorizedReq.Header.Set("Authorization", "Bearer dev-token")
	authorizedRec := httptest.NewRecorder()
	router.ServeHTTP(authorizedRec, authorizedReq)
	if authorizedRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", authorizedRec.Code, authorizedRec.Body.String())
	}

	var body apiEnvelope[map[string]any]
	decodeJSON(t, authorizedRec.Body.Bytes(), &body)

	status, ok := body.Data["status"].(map[string]any)
	if !ok {
		t.Fatalf("expected status object, got %+v", body.Data["status"])
	}
	if status["service"] != "ok" {
		t.Fatalf("expected service ok, got %+v", status["service"])
	}
	if status["db"] != "ok" {
		t.Fatalf("expected db ok, got %+v", status["db"])
	}
}

func TestLivenessDoesNotDependOnDatabase(t *testing.T) {
	router, _, gormDB := newTestRouterWithStorageRootAndDB(t, 1024)
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql db: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestLivenessDoesNotDependOnSharedRateLimitStore(t *testing.T) {
	router, _, _ := newTestRouterWithStorageRootAndDBConfigAndRateLimitStore(
		t,
		1024,
		func(cfg *config.Config) {
			cfg.RateLimitBackend = config.RateLimitBackendMySQL
		},
		failingRateLimitStore{},
	)

	livenessReq := httptest.NewRequest(http.MethodGet, "/livez", nil)
	livenessRec := httptest.NewRecorder()
	router.ServeHTTP(livenessRec, livenessReq)
	if livenessRec.Code != http.StatusOK {
		t.Fatalf("expected liveness 200, got %d, body=%s", livenessRec.Code, livenessRec.Body.String())
	}

	readinessReq := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readinessRec := httptest.NewRecorder()
	router.ServeHTTP(readinessRec, readinessReq)
	if readinessRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness 503, got %d, body=%s", readinessRec.Code, readinessRec.Body.String())
	}
	assertAPIErrorCode(t, readinessRec.Body.Bytes(), "rate_limit_unavailable")
}

func TestPublicReadinessProbeDoesNotUseGlobalIPRateLimit(t *testing.T) {
	router, _, _ := newTestRouterWithStorageRootAndDBConfigAndRateLimitStore(
		t,
		1024,
		func(cfg *config.Config) {
			cfg.RateLimitBackend = config.RateLimitBackendMySQL
		},
		healthOnlyRateLimitStore{},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/readyz expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	protectedReq := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
	protectedReq.Header.Set("Authorization", "Bearer dev-token")
	protectedRec := httptest.NewRecorder()
	router.ServeHTTP(protectedRec, protectedReq)
	if protectedRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected protected route to use global IP limiter and return 503, got %d, body=%s", protectedRec.Code, protectedRec.Body.String())
	}
	assertAPIErrorCode(t, protectedRec.Body.Bytes(), "rate_limit_unavailable")
}

func TestReadinessReturnsUnavailableWhenDependencyCheckFails(t *testing.T) {
	router, _, gormDB := newTestRouterWithStorageRootAndDB(t, 1024)
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql db: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d, body=%s", rec.Code, rec.Body.String())
	}
	assertAPIErrorCode(t, rec.Body.Bytes(), "not_ready")
}

func TestSystemMetricsRequiresAuthAndReportsRuntimeState(t *testing.T) {
	router := newTestRouter(t, 1024)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/metrics", nil)
	unauthorizedRec := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedRec, unauthorizedReq)
	if unauthorizedRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorizedRec.Code)
	}

	authorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/metrics", nil)
	authorizedReq.Header.Set("Authorization", "Bearer dev-token")
	authorizedRec := httptest.NewRecorder()
	router.ServeHTTP(authorizedRec, authorizedReq)
	if authorizedRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", authorizedRec.Code, authorizedRec.Body.String())
	}

	var body apiEnvelope[map[string]any]
	decodeJSON(t, authorizedRec.Body.Bytes(), &body)
	for _, section := range []string{"uploads", "transactions", "cleanup", "quota", "database", "rate_limit"} {
		if _, ok := body.Data[section]; !ok {
			t.Fatalf("expected metrics section %q, got %+v", section, body.Data)
		}
	}
}

func TestAuthenticatedHealthzReturnsUnavailableWhenDatabasePingFails(t *testing.T) {
	router, _, gormDB := newTestRouterWithStorageRootAndDB(t, 1024)
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql db: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body apiEnvelope[map[string]any]
	decodeJSON(t, rec.Body.Bytes(), &body)
	status, ok := body.Data["status"].(map[string]any)
	if !ok {
		t.Fatalf("expected status object, got %+v", body.Data["status"])
	}
	if status["db"] != "error" {
		t.Fatalf("expected db error, got %+v", status["db"])
	}
}
