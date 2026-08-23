package service

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
)

const defaultStorageQuotaMaxBytes uint64 = 10 * 1024 * 1024 * 1024

type StorageLimitStatus string

const (
	StorageLimitStatusOK       StorageLimitStatus = "ok"
	StorageLimitStatusWarning  StorageLimitStatus = "warning"
	StorageLimitStatusExceeded StorageLimitStatus = "exceeded"
)

type StorageQuotaSnapshot struct {
	RootPath       string
	UsedBytes      uint64
	ReservedBytes  uint64
	MaxBytes       uint64
	RemainingBytes uint64
	UsedPercent    float64
	LimitStatus    StorageLimitStatus
}

type StorageQuotaService struct {
	storageRoot string
	quotaRepo   *repository.StorageQuotaRepository
}

func NewStorageQuotaService(
	storageRoot string,
	quotaRepo *repository.StorageQuotaRepository,
) *StorageQuotaService {
	return &StorageQuotaService{
		storageRoot: storageRoot,
		quotaRepo:   quotaRepo,
	}
}

func (s *StorageQuotaService) Snapshot(ctx context.Context) (*StorageQuotaSnapshot, error) {
	return s.snapshot(ctx)
}

func (s *StorageQuotaService) UpdateMaxBytes(ctx context.Context, maxBytes uint64) (*StorageQuotaSnapshot, error) {
	quota, err := s.quotaRepo.UpdateMaxBytes(ctx, maxBytes)
	if err != nil {
		if errors.Is(err, repository.ErrStorageQuotaBelowUsage) {
			return nil, apperrors.New(http.StatusConflict, "storage_limit_below_usage", "storage limit cannot be lower than current usage")
		}
		return nil, apperrors.Wrap(http.StatusInternalServerError, "storage_limit_update_failed", "failed to update storage limit", err)
	}

	absStorageRoot, err := filepath.Abs(s.storageRoot)
	if err != nil {
		return nil, apperrors.Wrap(http.StatusInternalServerError, "storage_limit_unavailable", "failed to inspect storage usage", err)
	}
	return buildStorageQuotaSnapshotWithReserved(absStorageRoot, quota.UsedBytes, quota.ReservedBytes, quota.MaxBytes), nil
}

func (s *StorageQuotaService) snapshot(ctx context.Context) (*StorageQuotaSnapshot, error) {
	absStorageRoot, err := filepath.Abs(s.storageRoot)
	if err != nil {
		return nil, apperrors.Wrap(http.StatusInternalServerError, "storage_limit_unavailable", "failed to inspect storage usage", err)
	}

	quota, err := s.quotaRepo.EnsureDefault(ctx, defaultStorageQuotaMaxBytes)
	if err != nil {
		return nil, apperrors.Wrap(http.StatusInternalServerError, "storage_limit_unavailable", "failed to load storage limit", err)
	}

	return buildStorageQuotaSnapshotWithReserved(absStorageRoot, quota.UsedBytes, quota.ReservedBytes, quota.MaxBytes), nil
}

func buildStorageQuotaSnapshot(rootPath string, usedBytes uint64, maxBytes uint64) *StorageQuotaSnapshot {
	return buildStorageQuotaSnapshotWithReserved(rootPath, usedBytes, 0, maxBytes)
}

func buildStorageQuotaSnapshotWithReserved(rootPath string, usedBytes uint64, reservedBytes uint64, maxBytes uint64) *StorageQuotaSnapshot {
	remainingBytes := uint64(0)
	if usedBytes <= maxBytes && reservedBytes <= maxBytes-usedBytes {
		remainingBytes = maxBytes - usedBytes - reservedBytes
	}

	usedPercent := 0.0
	if maxBytes > 0 {
		usedPercent = float64(usedBytes) / float64(maxBytes) * 100
	}

	limitStatus := StorageLimitStatusOK
	switch {
	case maxBytes > 0 && usedBytes >= maxBytes:
		limitStatus = StorageLimitStatusExceeded
	case maxBytes > 0 && usedPercent >= 80:
		limitStatus = StorageLimitStatusWarning
	}

	return &StorageQuotaSnapshot{
		RootPath:       rootPath,
		UsedBytes:      usedBytes,
		ReservedBytes:  reservedBytes,
		MaxBytes:       maxBytes,
		RemainingBytes: remainingBytes,
		UsedPercent:    usedPercent,
		LimitStatus:    limitStatus,
	}
}
