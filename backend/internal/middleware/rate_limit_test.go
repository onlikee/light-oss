package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestDifferentInvalidBearerTokensShareClientIPLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ipLimiter := NewRateLimiter(0.000001, 2, time.Hour, 10)
	identityLimiter := NewRateLimiter(100, 10, time.Hour, 10)
	router := newTwoLevelRateLimitRouter(t, nil, ipLimiter, identityLimiter)

	wantStatuses := []int{http.StatusUnauthorized, http.StatusUnauthorized, http.StatusTooManyRequests}
	for i, wantStatus := range wantStatuses {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.RemoteAddr = "192.0.2.10:1234"
		req.Header.Set("Authorization", fmt.Sprintf("Bearer invalid-%d", i))
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("request %d: expected %d, got %d", i+1, wantStatus, rec.Code)
		}
		if wantStatus == http.StatusTooManyRequests && !strings.Contains(rec.Body.String(), `"code":"rate_limited"`) {
			t.Fatalf("request %d: expected stable rate_limited error, got %s", i+1, rec.Body.String())
		}
	}

	ipStats := ipLimiter.Stats()
	if ipStats.Entries != 1 || ipStats.RejectedRequests != 1 {
		t.Fatalf("unexpected IP limiter stats: %+v", ipStats)
	}
	if stats := identityLimiter.Stats(); stats.Entries != 0 {
		t.Fatalf("invalid tokens must not create identity entries: %+v", stats)
	}
}

func TestAuthenticatedBearerScopeLimitsAcrossClientIPs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ipLimiter := NewRateLimiter(100, 10, time.Hour, 10)
	identityLimiter := NewRateLimiter(0.000001, 2, time.Hour, 10)
	router := newTwoLevelRateLimitRouter(t, nil, ipLimiter, identityLimiter)

	wantStatuses := []int{http.StatusNoContent, http.StatusNoContent, http.StatusTooManyRequests}
	for i, wantStatus := range wantStatuses {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i+1)
		req.Header.Set("Authorization", "Bearer valid-token")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("request %d: expected %d, got %d", i+1, wantStatus, rec.Code)
		}
	}

	if stats := identityLimiter.Stats(); stats.Entries != 1 || stats.RejectedRequests != 1 {
		t.Fatalf("unexpected identity limiter stats: %+v", stats)
	}
}

func TestAuthenticatedRouteBudgetsAreIndependent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies() error = %v", err)
	}
	router.Use(NewRateLimiter(100, 10, time.Hour, 10).LimitByClientIP())

	protected := router.Group("")
	protected.Use(NewTokenValidator([]string{"valid-token"}).RequireBearer())
	for _, path := range []string{"/management", "/upload", "/sign"} {
		limiter := NewRateLimiter(0.000001, 1, time.Hour, 10)
		group := protected.Group("")
		group.Use(limiter.LimitByAuthenticatedBearer())
		group.GET(path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}

	for _, path := range []string{"/management", "/upload", "/sign"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("first request to %s: expected 204, got %d", path, rec.Code)
		}
	}

	for _, path := range []string{"/management", "/upload", "/sign"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("second request to %s: expected 429, got %d", path, rec.Code)
		}
	}
}

func TestRateLimiterDoesNotExceedMaxEntries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newRateLimiter(100, 10, time.Hour, 3, func() time.Time { return now })

	for i := 0; i < 10; i++ {
		limiter.getLimiter(fmt.Sprintf("key-%d", i))
		now = now.Add(time.Second)
	}

	stats := limiter.Stats()
	if stats.Entries != 3 || stats.MaxEntries != 3 {
		t.Fatalf("expected cache to stay at three entries, got %+v", stats)
	}
	if stats.CapacityEvictions != 7 {
		t.Fatalf("expected seven capacity evictions, got %+v", stats)
	}
}

func TestRateLimiterConcurrentAccessStaysBounded(t *testing.T) {
	limiter := NewRateLimiter(100, 10, time.Hour, 8)
	var waitGroup sync.WaitGroup
	for i := 0; i < 100; i++ {
		waitGroup.Add(1)
		go func(key string) {
			defer waitGroup.Done()
			_, _ = limiter.allow(context.Background(), key)
		}(fmt.Sprintf("key-%d", i))
	}
	waitGroup.Wait()

	stats := limiter.Stats()
	if stats.Entries != 8 || stats.CapacityEvictions != 92 {
		t.Fatalf("expected bounded concurrent cache, got %+v", stats)
	}
}

func TestSharedRateLimiterUsesStableNamespaceAndPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &fakeRateLimitStore{allowed: true}
	limiter := NewRateLimiterWithStore("upload", 3, 6, 2*time.Minute, store)
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies() error = %v", err)
	}
	router.Use(limiter.LimitByClientIP())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	call := store.singleCall(t)
	if call.key != "upload:ip:192.0.2.10" || call.rps != 3 || call.burst != 6 || call.ttl != 2*time.Minute {
		t.Fatalf("unexpected shared store call: %+v", call)
	}
	if stats := limiter.Stats(); stats.Backend != "mysql" || stats.Entries != 0 || stats.StoreErrors != 0 {
		t.Fatalf("unexpected shared limiter stats: %+v", stats)
	}
}

func TestSharedRateLimiterUsesAuthenticatedHashedScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &fakeRateLimitStore{allowed: true}
	identityLimiter := NewRateLimiterWithStore("management", 5, 10, time.Minute, store)
	router := newTwoLevelRateLimitRouter(
		t,
		nil,
		NewRateLimiter(100, 10, time.Hour, 10),
		identityLimiter,
	)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	call := store.singleCall(t)
	wantKey := "management:identity:" + bearerScope("valid-token")
	if call.key != wantKey || strings.Contains(call.key, "valid-token") {
		t.Fatalf("shared store key = %q, want irreversible authenticated scope", call.key)
	}
}

func TestSharedRateLimiterFailsClosedWhenStoreIsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &fakeRateLimitStore{err: errors.New("database unavailable")}
	limiter := NewRateLimiterWithStore("management", 5, 10, time.Minute, store)
	router := gin.New()
	router.Use(limiter.LimitByClientIP())
	handled := false
	router.GET("/", func(c *gin.Context) {
		handled = true
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if handled {
		t.Fatal("request must not reach the handler when the shared store fails")
	}
	if !strings.Contains(rec.Body.String(), `"code":"rate_limit_unavailable"`) {
		t.Fatalf("expected stable rate_limit_unavailable error, got %s", rec.Body.String())
	}
	if stats := limiter.Stats(); stats.StoreErrors != 1 || stats.RejectedRequests != 0 {
		t.Fatalf("unexpected failed-closed stats: %+v", stats)
	}
}

type fakeRateLimitStore struct {
	mu      sync.Mutex
	allowed bool
	err     error
	calls   []fakeRateLimitStoreCall
}

type fakeRateLimitStoreCall struct {
	key   string
	rps   float64
	burst int
	ttl   time.Duration
}

func (s *fakeRateLimitStore) Allow(_ context.Context, key string, rps float64, burst int, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, fakeRateLimitStoreCall{key: key, rps: rps, burst: burst, ttl: ttl})
	return s.allowed, s.err
}

func (s *fakeRateLimitStore) singleCall(t *testing.T) fakeRateLimitStoreCall {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) != 1 {
		t.Fatalf("expected one store call, got %d", len(s.calls))
	}
	return s.calls[0]
}

func TestRateLimiterLazilyEvictsExpiredEntries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newRateLimiter(100, 10, time.Minute, 10, func() time.Time { return now })

	limiter.getLimiter("old")
	now = now.Add(30 * time.Second)
	limiter.getLimiter("active")
	now = now.Add(31 * time.Second)
	stats := limiter.Stats()

	if stats.Entries != 1 || stats.ExpiredEvictions != 1 {
		t.Fatalf("expected one expired and one active entry, got %+v", stats)
	}
}

func TestClientIPTrustsForwardedHeaderOnlyForConfiguredProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		trustedProxies []string
		wantEntries    int
	}{
		{name: "untrusted remote", trustedProxies: nil, wantEntries: 1},
		{name: "trusted gateway", trustedProxies: []string{"192.0.2.10"}, wantEntries: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewRateLimiter(100, 10, time.Hour, 10)
			router := gin.New()
			if err := router.SetTrustedProxies(tt.trustedProxies); err != nil {
				t.Fatalf("SetTrustedProxies() error = %v", err)
			}
			router.Use(limiter.LimitByClientIP())
			router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

			for i := 1; i <= 2; i++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.RemoteAddr = "192.0.2.10:1234"
				req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusNoContent {
					t.Fatalf("request %d: expected 204, got %d", i, rec.Code)
				}
			}

			if stats := limiter.Stats(); stats.Entries != tt.wantEntries {
				t.Fatalf("expected %d entries, got %+v", tt.wantEntries, stats)
			}
		})
	}
}

func newTwoLevelRateLimitRouter(
	t *testing.T,
	trustedProxies []string,
	ipLimiter *RateLimiter,
	identityLimiter *RateLimiter,
) *gin.Engine {
	t.Helper()

	router := gin.New()
	if err := router.SetTrustedProxies(trustedProxies); err != nil {
		t.Fatalf("SetTrustedProxies() error = %v", err)
	}
	router.Use(ipLimiter.LimitByClientIP())

	protected := router.Group("")
	protected.Use(NewTokenValidator([]string{"valid-token"}).RequireBearer())
	protected.Use(identityLimiter.LimitByAuthenticatedBearer())
	protected.GET("/protected", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return router
}
