package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"light-oss/backend/internal/middleware"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
	"light-oss/backend/internal/service"
)

type updateStorageQuotaRequest struct {
	MaxBytes *int64 `json:"max_bytes"`
}

type systemStatsResponse struct {
	OS      string                `json:"os"`
	CPU     systemCPUResponse     `json:"cpu"`
	Memory  systemMemoryResponse  `json:"memory"`
	Disks   []systemDiskResponse  `json:"disks"`
	Storage systemStorageResponse `json:"storage"`
}

type systemCPUResponse struct {
	UsedPercent float64 `json:"used_percent"`
}

type systemMemoryResponse struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

type systemDiskResponse struct {
	Label               string  `json:"label"`
	MountPoint          string  `json:"mount_point"`
	Filesystem          string  `json:"filesystem"`
	TotalBytes          uint64  `json:"total_bytes"`
	UsedBytes           uint64  `json:"used_bytes"`
	FreeBytes           uint64  `json:"free_bytes"`
	UsedPercent         float64 `json:"used_percent"`
	ContainsStorageRoot bool    `json:"contains_storage_root"`
}

type systemStorageResponse struct {
	RootPath       string  `json:"root_path"`
	UsedBytes      uint64  `json:"used_bytes"`
	MaxBytes       uint64  `json:"max_bytes"`
	RemainingBytes uint64  `json:"remaining_bytes"`
	UsedPercent    float64 `json:"used_percent"`
	LimitStatus    string  `json:"limit_status"`
}

func (h *apiHandler) livez(c *gin.Context) {
	response.JSON(c, http.StatusOK, gin.H{
		"status":  "ok",
		"version": "mvp",
	})
}

func (h *apiHandler) readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	var err error
	if h.readinessCheck != nil {
		err = h.readinessCheck(ctx)
	} else {
		err = h.db.PingContext(ctx)
	}
	if err != nil {
		response.Error(c, apperrors.Wrap(
			http.StatusServiceUnavailable,
			"not_ready",
			"service is not ready",
			err,
		))
		return
	}

	response.JSON(c, http.StatusOK, gin.H{
		"status":  "ready",
		"version": "mvp",
	})
}

func (h *apiHandler) healthz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	dbStatus := "ok"
	statusCode := http.StatusOK
	if err := h.db.PingContext(ctx); err != nil {
		dbStatus = "error"
		statusCode = http.StatusServiceUnavailable
		h.logger.Error("healthz database ping failed", zap.Error(err))
	}

	response.JSON(c, statusCode, gin.H{
		"status": gin.H{
			"service": "ok",
			"db":      dbStatus,
		},
		"version": "mvp",
	})
}

func (h *apiHandler) systemMetrics(c *gin.Context) {
	runtimeSnapshot := h.runtimeMetrics.Snapshot()
	dbStats := h.db.Stats()
	quota, err := h.storageQuotaService.Snapshot(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, gin.H{
		"uploads": gin.H{
			"staged":               runtimeSnapshot.UploadsStaged,
			"failed":               runtimeSnapshot.UploadsFailed,
			"bytes":                runtimeSnapshot.UploadBytes,
			"staging_duration_ns":  runtimeSnapshot.UploadStagingDurationNanos,
			"reservation_failures": runtimeSnapshot.ReservationFailures,
		},
		"transactions": gin.H{
			"completed":   runtimeSnapshot.TransactionsCompleted,
			"failed":      runtimeSnapshot.TransactionsFailed,
			"duration_ns": runtimeSnapshot.TransactionDurationNanos,
		},
		"cleanup": gin.H{
			"completed": runtimeSnapshot.CleanupCompleted,
			"failed":    runtimeSnapshot.CleanupFailed,
			"backlog":   runtimeSnapshot.CleanupBacklog,
		},
		"quota": gin.H{
			"used_bytes":      quota.UsedBytes,
			"reserved_bytes":  quota.ReservedBytes,
			"max_bytes":       quota.MaxBytes,
			"remaining_bytes": quota.RemainingBytes,
		},
		"database": gin.H{
			"max_open_connections": dbStats.MaxOpenConnections,
			"open_connections":     dbStats.OpenConnections,
			"in_use":               dbStats.InUse,
			"idle":                 dbStats.Idle,
			"wait_count":           dbStats.WaitCount,
			"wait_duration_ns":     dbStats.WaitDuration.Nanoseconds(),
			"max_idle_closed":      dbStats.MaxIdleClosed,
			"max_idle_time_closed": dbStats.MaxIdleTimeClosed,
			"max_lifetime_closed":  dbStats.MaxLifetimeClosed,
		},
		"rate_limit": gin.H{
			"ip":         rateLimiterStats(c.Request.Context(), h.ipRateLimiter),
			"public":     rateLimiterStats(c.Request.Context(), h.publicRateLimiter),
			"management": rateLimiterStats(c.Request.Context(), h.managementLimiter),
			"upload":     rateLimiterStats(c.Request.Context(), h.uploadRateLimiter),
			"sign":       rateLimiterStats(c.Request.Context(), h.signRateLimiter),
			"health":     rateLimiterStats(c.Request.Context(), h.healthRateLimiter),
		},
	})
}

func rateLimiterStats(ctx context.Context, limiter *middleware.RateLimiter) gin.H {
	stats := limiter.StatsContext(ctx)
	return gin.H{
		"backend":             stats.Backend,
		"entries":             stats.Entries,
		"max_entries":         stats.MaxEntries,
		"expired_evictions":   stats.ExpiredEvictions,
		"capacity_evictions":  stats.CapacityEvictions,
		"capacity_rejections": stats.CapacityRejections,
		"rejected_requests":   stats.RejectedRequests,
		"store_errors":        stats.StoreErrors,
	}
}

func (h *apiHandler) systemStats(c *gin.Context) {
	stats, err := h.systemStatsService.Collect(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, systemStatsToResponse(*stats))
}

func (h *apiHandler) updateStorageQuota(c *gin.Context) {
	var req updateStorageQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "request body is invalid"))
		return
	}
	if req.MaxBytes == nil || *req.MaxBytes <= 0 {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "max_bytes must be greater than zero"))
		return
	}

	quota, err := h.storageQuotaService.UpdateMaxBytes(c.Request.Context(), uint64(*req.MaxBytes))
	if err != nil {
		response.Error(c, err)
		return
	}

	response.JSON(c, http.StatusOK, storageQuotaToResponse(*quota))
}

func systemStatsToResponse(stats service.SystemStats) systemStatsResponse {
	disks := make([]systemDiskResponse, 0, len(stats.Disks))
	for _, item := range stats.Disks {
		disks = append(disks, systemDiskResponse{
			Label:               item.Label,
			MountPoint:          item.MountPoint,
			Filesystem:          item.Filesystem,
			TotalBytes:          item.TotalBytes,
			UsedBytes:           item.UsedBytes,
			FreeBytes:           item.FreeBytes,
			UsedPercent:         item.UsedPercent,
			ContainsStorageRoot: item.ContainsStorageRoot,
		})
	}

	return systemStatsResponse{
		OS: stats.OS,
		CPU: systemCPUResponse{
			UsedPercent: stats.CPU.UsedPercent,
		},
		Memory: systemMemoryResponse{
			TotalBytes:     stats.Memory.TotalBytes,
			UsedBytes:      stats.Memory.UsedBytes,
			AvailableBytes: stats.Memory.AvailableBytes,
			UsedPercent:    stats.Memory.UsedPercent,
		},
		Disks: disks,
		Storage: systemStorageResponse{
			RootPath:       stats.Storage.RootPath,
			UsedBytes:      stats.Storage.UsedBytes,
			MaxBytes:       stats.Storage.MaxBytes,
			RemainingBytes: stats.Storage.RemainingBytes,
			UsedPercent:    stats.Storage.UsedPercent,
			LimitStatus:    string(stats.Storage.LimitStatus),
		},
	}
}

func storageQuotaToResponse(stats service.StorageQuotaSnapshot) systemStorageResponse {
	return systemStorageResponse{
		RootPath:       stats.RootPath,
		UsedBytes:      stats.UsedBytes,
		MaxBytes:       stats.MaxBytes,
		RemainingBytes: stats.RemainingBytes,
		UsedPercent:    stats.UsedPercent,
		LimitStatus:    string(stats.LimitStatus),
	}
}
