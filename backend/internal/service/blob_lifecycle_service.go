package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/storage"
)

const (
	defaultReservationChunkBytes uint64 = 8 * 1024 * 1024
	blobLedgerWriteBatchSize            = 100
	batchStagingConcurrency             = 16
)

type BlobStore interface {
	Stage(context.Context, string, io.Reader, func(int64) error) (*storage.StoredFile, error)
	Commit(string, string) error
	Delete(string) error
}

type BlobReader interface {
	Open(string) (io.ReadCloser, error)
}

type StagedBlob struct {
	ID          string
	StoragePath string
	StagingPath string
	Size        uint64
	ETag        string
	lease       *stagingLeaseHeartbeat
}

type stagingLeaseHeartbeat struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	err      error
}

func (h *stagingLeaseHeartbeat) stop() {
	if h == nil {
		return
	}
	h.stopOnce.Do(h.cancel)
	<-h.done
}

func (h *stagingLeaseHeartbeat) fail(err error) {
	h.mu.Lock()
	if h.err == nil {
		h.err = err
	}
	h.mu.Unlock()
	h.cancel()
}

func (h *stagingLeaseHeartbeat) Err() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

type BlobBatchInput struct {
	Size uint64
	Open func() (io.ReadCloser, error)
}

type batchBlobReaderError struct {
	operation string
	err       error
}

type publishOutcome uint8

const (
	publishOutcomeUnknown publishOutcome = iota
	publishOutcomeRolledBack
	publishOutcomeCommitted
)

func (e *batchBlobReaderError) Error() string {
	return fmt.Sprintf("%s batch blob reader: %v", e.operation, e.err)
}

func (e *batchBlobReaderError) Unwrap() error {
	return e.err
}

type BlobLifecycleService struct {
	logger           *zap.Logger
	gormDB           *gorm.DB
	store            BlobStore
	blobRepo         *repository.StorageBlobRepository
	reservationChunk uint64
	stagingLease     time.Duration
	wakeCleanup      func()
	runCleanup       func(context.Context) error
	metrics          *RuntimeMetrics
}

func NewBlobLifecycleService(
	logger *zap.Logger,
	gormDB *gorm.DB,
	store BlobStore,
	blobRepo *repository.StorageBlobRepository,
	reservationChunk uint64,
) *BlobLifecycleService {
	if reservationChunk == 0 {
		reservationChunk = defaultReservationChunkBytes
	}

	return &BlobLifecycleService{
		logger:           logger,
		gormDB:           gormDB,
		store:            store,
		blobRepo:         blobRepo,
		reservationChunk: reservationChunk,
		stagingLease:     defaultStagingTTL,
	}
}

func (s *BlobLifecycleService) SetStagingLease(lease time.Duration) {
	if lease > 0 {
		s.stagingLease = lease
	}
}

func (s *BlobLifecycleService) SetCleanupWake(wake func()) {
	s.wakeCleanup = wake
}

func (s *BlobLifecycleService) SetCleanupRunOnce(run func(context.Context) error) {
	s.runCleanup = run
}

func (s *BlobLifecycleService) SetMetrics(metrics *RuntimeMetrics) {
	s.metrics = metrics
}

func (s *BlobLifecycleService) Stage(ctx context.Context, reader io.Reader) (result *StagedBlob, resultErr error) {
	startedAt := time.Now()
	defer func() {
		var size uint64
		if result != nil {
			size = result.Size
		}
		s.metrics.RecordUploadStaging(size, time.Since(startedAt), resultErr)
	}()

	blobID := uuid.NewString()
	stagingPath := storage.NewManagedPath("staging", ".tmp")
	storagePath := storage.NewManagedPath("objects", ".bin")
	blob := &model.StorageBlob{
		ID:          blobID,
		StoragePath: storagePath,
		StagingPath: &stagingPath,
		Status:      model.StorageBlobStatusStaging,
	}
	if err := s.blobRepo.CreateStagingWithLease(ctx, blob, s.stagingLease); err != nil {
		return nil, err
	}
	lease := s.startStagingLeaseHeartbeat(ctx, []string{blobID})
	abort := func() {
		lease.stop()
		s.abortStaging(context.WithoutCancel(ctx), blob)
	}

	reservedBytes := uint64(0)
	reserveBeforeWrite := func(totalBytes int64) error {
		if totalBytes <= 0 || uint64(totalBytes) <= reservedBytes {
			return nil
		}

		requiredBytes := roundUp(uint64(totalBytes), s.reservationChunk)
		delta := requiredBytes - reservedBytes
		if err := s.blobRepo.Reserve(lease.ctx, blobID, delta); err != nil {
			if !errors.Is(err, repository.ErrStorageQuotaExceeded) {
				return err
			}

			delta = uint64(totalBytes) - reservedBytes
			if err := s.blobRepo.Reserve(lease.ctx, blobID, delta); err != nil {
				if errors.Is(err, repository.ErrStorageQuotaExceeded) {
					s.metrics.RecordReservationFailure()
				}
				return err
			}
			requiredBytes = uint64(totalBytes)
		}

		reservedBytes = requiredBytes
		return nil
	}

	stagedFile, err := s.store.Stage(lease.ctx, stagingPath, reader, reserveBeforeWrite)
	if err != nil {
		leaseErr := lease.Err()
		abort()
		if errors.Is(err, repository.ErrStorageQuotaExceeded) {
			return nil, apperrors.New(http.StatusInsufficientStorage, "storage_limit_exceeded", "storage usage exceeds configured limit")
		}
		if leaseErr != nil {
			return nil, errors.Join(err, fmt.Errorf("renew staging lease: %w", leaseErr))
		}
		return nil, err
	}
	if stagedFile.Size == 0 && reservedBytes == 0 {
		if err := s.blobRepo.Reserve(lease.ctx, blobID, 1); err != nil {
			if errors.Is(err, repository.ErrStorageQuotaExceeded) {
				s.metrics.RecordReservationFailure()
			}
			abort()
			if errors.Is(err, repository.ErrStorageQuotaExceeded) {
				return nil, apperrors.New(http.StatusInsufficientStorage, "storage_limit_exceeded", "storage usage exceeds configured limit")
			}
			return nil, err
		}
	}

	if err := s.store.Commit(stagingPath, storagePath); err != nil {
		abort()
		return nil, err
	}
	if leaseErr := lease.Err(); leaseErr != nil {
		abort()
		return nil, fmt.Errorf("renew staging lease: %w", leaseErr)
	}

	result = &StagedBlob{
		ID:          blobID,
		StoragePath: storagePath,
		StagingPath: stagingPath,
		Size:        uint64(stagedFile.Size),
		ETag:        stagedFile.ETag,
		lease:       lease,
	}
	s.logger.Info(
		"object content staged",
		zap.String("blob_id", result.ID),
		zap.String("storage_path", result.StoragePath),
		zap.Uint64("size_bytes", result.Size),
		zap.Duration("duration", time.Since(startedAt)),
	)
	return result, nil
}

func roundUp(value uint64, block uint64) uint64 {
	if block == 0 || value == 0 {
		return value
	}
	return ((value-1)/block + 1) * block
}

func stagedBlobStoreError(err error) error {
	if appErr := apperrors.From(err); appErr.Code != "internal_error" {
		return err
	}
	return apperrors.Wrap(http.StatusInternalServerError, "object_store_failed", "failed to store object", fmt.Errorf("store staged blob: %w", err))
}
