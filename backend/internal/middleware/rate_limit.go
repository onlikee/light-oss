package middleware

import (
	"container/list"
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
)

type RateLimitStore interface {
	Allow(ctx context.Context, key string, rps float64, burst int, ttl time.Duration) (bool, error)
}

type rateLimitStoreStatsProvider interface {
	Stats(context.Context) (RateLimitStoreStats, error)
}

type RateLimiter struct {
	backend         string
	namespace       string
	store           RateLimitStore
	rps             rate.Limit
	burst           int
	ttl             time.Duration
	maxEntries      int
	now             func() time.Time
	mu              sync.Mutex
	limiters        map[string]*rateLimiterEntry
	lastSeenOrder   *list.List
	nextCleanup     time.Time
	expiredEvicted  uint64
	capacityEvicted uint64
	rejected        uint64
	storeErrors     uint64
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
	order    *list.Element
}

type RateLimiterStats struct {
	Backend            string
	Entries            int
	MaxEntries         int
	ExpiredEvictions   uint64
	CapacityEvictions  uint64
	CapacityRejections uint64
	RejectedRequests   uint64
	StoreErrors        uint64
}

func NewRateLimiter(rps float64, burst int, ttl time.Duration, maxEntries int) *RateLimiter {
	return newRateLimiter(rps, burst, ttl, maxEntries, time.Now)
}

func newRateLimiter(rps float64, burst int, ttl time.Duration, maxEntries int, now func() time.Time) *RateLimiter {
	currentTime := now()
	return &RateLimiter{
		backend:       "local",
		rps:           rate.Limit(rps),
		burst:         burst,
		ttl:           ttl,
		maxEntries:    maxEntries,
		now:           now,
		limiters:      make(map[string]*rateLimiterEntry),
		lastSeenOrder: list.New(),
		nextCleanup:   currentTime.Add(ttl),
	}
}

func NewRateLimiterWithStore(
	namespace string,
	rps float64,
	burst int,
	ttl time.Duration,
	store RateLimitStore,
) *RateLimiter {
	if store == nil {
		panic("rate limit store is required")
	}
	now := time.Now()
	return &RateLimiter{
		backend:       "mysql",
		namespace:     namespace,
		store:         store,
		rps:           rate.Limit(rps),
		burst:         burst,
		ttl:           ttl,
		now:           time.Now,
		limiters:      make(map[string]*rateLimiterEntry),
		lastSeenOrder: list.New(),
		nextCleanup:   now.Add(ttl),
	}
}

func (r *RateLimiter) LimitByClientIP() gin.HandlerFunc {
	return r.middleware(func(c *gin.Context) (string, bool) {
		return "ip:" + c.ClientIP(), true
	})
}

func (r *RateLimiter) LimitByAuthenticatedBearer() gin.HandlerFunc {
	return r.middleware(func(c *gin.Context) (string, bool) {
		scope, ok := AuthenticatedBearerScope(c)
		return "identity:" + scope, ok
	})
}

func (r *RateLimiter) middleware(key func(*gin.Context) (string, bool)) gin.HandlerFunc {
	return func(c *gin.Context) {
		limiterKey, ok := key(c)
		if !ok {
			response.Error(c, apperrors.New(http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token"))
			c.Abort()
			return
		}

		allowed, err := r.allow(c.Request.Context(), limiterKey)
		if err != nil {
			response.Error(c, apperrors.Wrap(http.StatusServiceUnavailable, "rate_limit_unavailable", "rate limit unavailable", err))
			c.Abort()
			return
		}

		if !allowed {
			response.Error(c, apperrors.New(http.StatusTooManyRequests, "rate_limited", "rate limit exceeded"))
			c.Abort()
			return
		}

		c.Next()
	}
}

func (r *RateLimiter) allow(ctx context.Context, key string) (bool, error) {
	if r.store != nil {
		allowed, err := r.store.Allow(ctx, r.namespace+":"+key, float64(r.rps), r.burst, r.ttl)
		r.mu.Lock()
		defer r.mu.Unlock()
		if err != nil {
			r.storeErrors++
			return false, err
		}
		if !allowed {
			r.rejected++
		}
		return allowed, nil
	}

	limiter := r.getLimiter(key)
	allowed := limiter.Allow()
	if !allowed {
		r.mu.Lock()
		r.rejected++
		r.mu.Unlock()
	}
	return allowed, nil
}

func (r *RateLimiter) getLimiter(key string) *rate.Limiter {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if !now.Before(r.nextCleanup) {
		r.evictExpired(now)
		r.nextCleanup = now.Add(r.ttl)
	}

	if entry, ok := r.limiters[key]; ok {
		if now.Sub(entry.lastSeen) < r.ttl {
			entry.lastSeen = now
			r.lastSeenOrder.MoveToBack(entry.order)
			return entry.limiter
		}

		r.removeEntry(key, entry)
		r.expiredEvicted++
	}

	if len(r.limiters) >= r.maxEntries {
		r.evictOldest()
	}

	limiter := rate.NewLimiter(r.rps, r.burst)
	order := r.lastSeenOrder.PushBack(key)
	r.limiters[key] = &rateLimiterEntry{
		limiter:  limiter,
		lastSeen: now,
		order:    order,
	}
	return limiter
}

func (r *RateLimiter) Stats() RateLimiterStats {
	return r.StatsContext(context.Background())
}

func (r *RateLimiter) StatsContext(ctx context.Context) RateLimiterStats {
	r.mu.Lock()

	now := r.now()
	if !now.Before(r.nextCleanup) {
		r.evictExpired(now)
		r.nextCleanup = now.Add(r.ttl)
	}

	stats := RateLimiterStats{
		Backend:           r.backend,
		Entries:           len(r.limiters),
		MaxEntries:        r.maxEntries,
		ExpiredEvictions:  r.expiredEvicted,
		CapacityEvictions: r.capacityEvicted,
		RejectedRequests:  r.rejected,
		StoreErrors:       r.storeErrors,
	}
	r.mu.Unlock()

	provider, ok := r.store.(rateLimitStoreStatsProvider)
	if !ok {
		return stats
	}
	storeStats, err := provider.Stats(ctx)
	if err != nil {
		r.mu.Lock()
		r.storeErrors++
		stats.StoreErrors = r.storeErrors
		r.mu.Unlock()
		return stats
	}
	stats.Entries = storeStats.Entries
	stats.MaxEntries = storeStats.MaxEntries
	stats.ExpiredEvictions = storeStats.ExpiredEvictions
	stats.CapacityRejections = storeStats.CapacityRejections
	return stats
}

func (r *RateLimiter) evictExpired(now time.Time) {
	for element := r.lastSeenOrder.Front(); element != nil; element = r.lastSeenOrder.Front() {
		key := element.Value.(string)
		entry := r.limiters[key]
		if now.Sub(entry.lastSeen) < r.ttl {
			return
		}

		r.removeEntry(key, entry)
		r.expiredEvicted++
	}
}

func (r *RateLimiter) evictOldest() {
	oldest := r.lastSeenOrder.Front()
	key := oldest.Value.(string)
	r.removeEntry(key, r.limiters[key])
	r.capacityEvicted++
}

func (r *RateLimiter) removeEntry(key string, entry *rateLimiterEntry) {
	delete(r.limiters, key)
	r.lastSeenOrder.Remove(entry.order)
}
