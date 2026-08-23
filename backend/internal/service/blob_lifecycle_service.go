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
	"gorm.io/gorm/clause"

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

func (s *BlobLifecycleService) StageKnownBatch(ctx context.Context, inputs []BlobBatchInput) ([]*StagedBlob, error) {
	if len(inputs) == 0 {
		return []*StagedBlob{}, nil
	}

	blobs := make([]model.StorageBlob, 0, len(inputs))
	stagedBlobs := make([]*StagedBlob, 0, len(inputs))
	for _, input := range inputs {
		blobID := uuid.NewString()
		stagingPath := storage.NewManagedPath("staging", ".tmp")
		storagePath := storage.NewManagedPath("objects", ".bin")
		reservedSize := input.Size
		if reservedSize == 0 {
			reservedSize = 1
		}
		blobs = append(blobs, model.StorageBlob{
			ID:          blobID,
			StoragePath: storagePath,
			StagingPath: &stagingPath,
			Size:        reservedSize,
			Status:      model.StorageBlobStatusStaging,
		})
		stagedBlobs = append(stagedBlobs, &StagedBlob{
			ID:          blobID,
			StoragePath: storagePath,
			StagingPath: stagingPath,
		})
	}

	if err := s.blobRepo.CreateStagingBatchWithLease(ctx, blobs, blobLedgerWriteBatchSize, s.stagingLease); err != nil {
		if errors.Is(err, repository.ErrStorageQuotaExceeded) {
			s.metrics.RecordReservationFailure()
			return nil, apperrors.New(http.StatusInsufficientStorage, "storage_limit_exceeded", "storage usage exceeds configured limit")
		}
		return nil, err
	}
	blobIDs := make([]string, 0, len(stagedBlobs))
	for _, staged := range stagedBlobs {
		blobIDs = append(blobIDs, staged.ID)
	}
	lease := s.startStagingLeaseHeartbeat(ctx, blobIDs)
	for _, staged := range stagedBlobs {
		staged.lease = lease
	}

	batchCtx, cancel := context.WithCancel(lease.ctx)
	defer cancel()

	indices := make(chan int, len(inputs))
	for index := range inputs {
		indices <- index
	}
	close(indices)

	var firstErr error
	var firstErrOnce sync.Once
	recordError := func(err error) {
		firstErrOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}

	workerCount := min(batchStagingConcurrency, len(inputs))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range indices {
				if batchCtx.Err() != nil {
					return
				}

				input := inputs[index]
				if input.Open == nil {
					recordError(&batchBlobReaderError{operation: "open", err: fmt.Errorf("batch blob reader is not configured")})
					return
				}
				reader, err := input.Open()
				if err != nil {
					recordError(&batchBlobReaderError{operation: "open", err: err})
					return
				}

				staged, stageErr := s.stageKnown(batchCtx, stagedBlobs[index], input.Size, reader)
				closeErr := reader.Close()
				if stageErr != nil {
					recordError(stageErr)
					return
				}
				if closeErr != nil {
					recordError(&batchBlobReaderError{operation: "close", err: closeErr})
					return
				}
				stagedBlobs[index] = staged
			}
		}()
	}
	workers.Wait()

	if firstErr != nil {
		s.Abort(ctx, stagedBlobs...)
		if leaseErr := lease.Err(); leaseErr != nil {
			return nil, errors.Join(firstErr, fmt.Errorf("renew staging lease: %w", leaseErr))
		}
		return nil, firstErr
	}
	if leaseErr := lease.Err(); leaseErr != nil {
		s.Abort(ctx, stagedBlobs...)
		return nil, fmt.Errorf("renew staging lease: %w", leaseErr)
	}

	return stagedBlobs, nil
}

func (s *BlobLifecycleService) stageKnown(
	ctx context.Context,
	prepared *StagedBlob,
	expectedSize uint64,
	reader io.Reader,
) (result *StagedBlob, resultErr error) {
	startedAt := time.Now()
	defer func() {
		var size uint64
		if result != nil {
			size = result.Size
		}
		s.metrics.RecordUploadStaging(size, time.Since(startedAt), resultErr)
	}()

	stagedFile, err := s.store.Stage(ctx, prepared.StagingPath, reader, func(totalBytes int64) error {
		if totalBytes < 0 || uint64(totalBytes) > expectedSize {
			return fmt.Errorf("staged blob exceeds declared size %d", expectedSize)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if stagedFile.Size < 0 || uint64(stagedFile.Size) != expectedSize {
		return nil, fmt.Errorf("staged blob size %d does not match declared size %d", stagedFile.Size, expectedSize)
	}
	if err := s.store.Commit(prepared.StagingPath, prepared.StoragePath); err != nil {
		return nil, err
	}

	result = &StagedBlob{
		ID:          prepared.ID,
		StoragePath: prepared.StoragePath,
		StagingPath: prepared.StagingPath,
		Size:        expectedSize,
		ETag:        stagedFile.ETag,
		lease:       prepared.lease,
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

func (s *BlobLifecycleService) Publish(
	ctx context.Context,
	stagedBlobs []*StagedBlob,
	publishMetadata func(*gorm.DB) ([]string, error),
) error {
	if leaseErr := stagedBlobsLeaseError(stagedBlobs); leaseErr != nil {
		stopStagedBlobLeases(stagedBlobs)
		s.triggerCleanup(ctx)
		return fmt.Errorf("renew staging lease: %w", leaseErr)
	}
	defer stopStagedBlobLeases(stagedBlobs)

	cleanupQueued := false
	transactionStartedAt := time.Now()
	err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		blobRepo := s.blobRepo.WithDB(tx)
		activations := make([]repository.StorageBlobActivation, 0, len(stagedBlobs))
		for _, staged := range stagedBlobs {
			activations = append(activations, repository.StorageBlobActivation{
				ID:         staged.ID,
				ActualSize: staged.Size,
			})
		}
		if err := blobRepo.ActivateStagingBatch(ctx, activations, blobLedgerWriteBatchSize); err != nil {
			return err
		}

		var releasedPaths []string
		if publishMetadata != nil {
			var err error
			releasedPaths, err = publishMetadata(tx)
			if err != nil {
				return err
			}
		}

		if len(releasedPaths) > 0 {
			if err := blobRepo.ReleaseReferences(ctx, releasedPaths, time.Now().UTC(), blobLedgerWriteBatchSize); err != nil {
				return err
			}
			cleanupQueued = true
		}

		return nil
	})
	s.metrics.RecordTransaction(time.Since(transactionStartedAt), err)
	if err != nil {
		if len(stagedBlobs) == 0 {
			return err
		}

		confirmationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		outcome, confirmationErr := s.confirmPublishOutcome(confirmationCtx, stagedBlobs)
		cancel()
		switch outcome {
		case publishOutcomeCommitted:
			s.logger.Error(
				"activated storage blobs observed after transaction error; preserving committed state",
				zap.Int("blob_count", len(stagedBlobs)),
				zap.Error(err),
			)
			s.triggerCleanup(ctx)
			return errors.Join(err, fmt.Errorf("storage publish result is uncertain; activated blobs were preserved"))
		case publishOutcomeRolledBack:
			s.triggerCleanup(ctx)
			return err
		default:
			s.logger.Error(
				"storage publish outcome is uncertain; preserving tracked blobs for recovery",
				zap.Int("blob_count", len(stagedBlobs)),
				zap.Error(err),
				zap.NamedError("confirmation_error", confirmationErr),
			)
			return errors.Join(err, fmt.Errorf("confirm storage publish outcome: %w", confirmationErr))
		}
	}

	if cleanupQueued {
		if s.runCleanup != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := s.runCleanup(cleanupCtx); err != nil {
				s.logger.Warn("immediate storage cleanup failed", zap.Error(err))
			}
			cancel()
		}
		if s.wakeCleanup != nil {
			s.wakeCleanup()
		}
	}
	return nil
}

func (s *BlobLifecycleService) confirmPublishOutcome(
	ctx context.Context,
	stagedBlobs []*StagedBlob,
) (publishOutcome, error) {
	expectedByID := make(map[string]*StagedBlob, len(stagedBlobs))
	ids := make([]string, 0, len(stagedBlobs))
	for _, staged := range stagedBlobs {
		if staged == nil || staged.ID == "" {
			return publishOutcomeUnknown, fmt.Errorf("staged blob identity is missing")
		}
		if _, exists := expectedByID[staged.ID]; exists {
			return publishOutcomeUnknown, fmt.Errorf("staged blob %s is duplicated", staged.ID)
		}
		expectedByID[staged.ID] = staged
		ids = append(ids, staged.ID)
	}

	var outcome publishOutcome
	err := s.gormDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		blobsByID := make(map[string]model.StorageBlob, len(ids))
		for start := 0; start < len(ids); start += blobLedgerWriteBatchSize {
			end := min(start+blobLedgerWriteBatchSize, len(ids))
			var blobs []model.StorageBlob
			if err := tx.WithContext(ctx).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id IN ?", ids[start:end]).
				Find(&blobs).Error; err != nil {
				return err
			}
			for _, blob := range blobs {
				blobsByID[blob.ID] = blob
			}
		}
		if len(blobsByID) != len(ids) {
			return fmt.Errorf("storage publish confirmation is missing blob rows")
		}

		allCommitted := true
		allRolledBack := true
		for _, id := range ids {
			blob := blobsByID[id]
			expected := expectedByID[id]
			committed := blob.Status == model.StorageBlobStatusActive &&
				blob.StoragePath == expected.StoragePath &&
				blob.Size == expected.Size &&
				blob.RefCount == 1 &&
				blob.StagingPath == nil
			rolledBack := blob.Status == model.StorageBlobStatusStaging &&
				blob.StoragePath == expected.StoragePath &&
				blob.StagingPath != nil &&
				*blob.StagingPath == expected.StagingPath &&
				blob.RefCount == 0
			allCommitted = allCommitted && committed
			allRolledBack = allRolledBack && rolledBack
		}

		switch {
		case allCommitted:
			outcome = publishOutcomeCommitted
			return nil
		case allRolledBack:
			now := time.Now().UTC()
			txRepo := s.blobRepo.WithDB(tx)
			if err := txRepo.SealStagingForCleanupBatch(ctx, ids, now, blobLedgerWriteBatchSize); err != nil {
				return err
			}
			jobs := make([]model.StorageCleanupJob, 0, len(stagedBlobs))
			for _, staged := range stagedBlobs {
				jobs = append(jobs, model.StorageCleanupJob{
					BlobID:      staged.ID,
					StoragePath: staged.StoragePath,
					NextRetryAt: now,
					LastError:   "storage publish transaction rolled back",
				})
			}
			if err := txRepo.EnqueueCleanupBatch(ctx, jobs, blobLedgerWriteBatchSize); err != nil {
				return err
			}
			outcome = publishOutcomeRolledBack
			return nil
		default:
			return fmt.Errorf("storage publish confirmation found mixed blob states")
		}
	})
	if err != nil {
		return publishOutcomeUnknown, err
	}
	return outcome, nil
}

func (s *BlobLifecycleService) triggerCleanup(ctx context.Context) {
	if s.runCleanup != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if err := s.runCleanup(cleanupCtx); err != nil {
			s.logger.Warn("immediate storage cleanup failed", zap.Error(err))
		}
		cancel()
	}
	if s.wakeCleanup != nil {
		s.wakeCleanup()
	}
}

func (s *BlobLifecycleService) Abort(ctx context.Context, stagedBlobs ...*StagedBlob) {
	stopStagedBlobLeases(stagedBlobs)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	s.abortBatch(cleanupCtx, stagedBlobs)
}

func (s *BlobLifecycleService) startStagingLeaseHeartbeat(
	ctx context.Context,
	blobIDs []string,
) *stagingLeaseHeartbeat {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	heartbeat := &stagingLeaseHeartbeat{
		ctx:    heartbeatCtx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	interval := s.stagingLease / 3
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	go func() {
		defer close(heartbeat.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := s.blobRepo.RenewStagingLease(
					heartbeatCtx,
					blobIDs,
					s.stagingLease,
					blobLedgerWriteBatchSize,
				); err != nil {
					heartbeat.fail(err)
					return
				}
			}
		}
	}()
	return heartbeat
}

func stopStagedBlobLeases(stagedBlobs []*StagedBlob) {
	seen := make(map[*stagingLeaseHeartbeat]struct{})
	for _, staged := range stagedBlobs {
		if staged == nil || staged.lease == nil {
			continue
		}
		if _, exists := seen[staged.lease]; exists {
			continue
		}
		seen[staged.lease] = struct{}{}
		staged.lease.stop()
	}
}

func stagedBlobsLeaseError(stagedBlobs []*StagedBlob) error {
	for _, staged := range stagedBlobs {
		if staged == nil || staged.lease == nil {
			continue
		}
		if err := staged.lease.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *BlobLifecycleService) abortStaging(ctx context.Context, blob *model.StorageBlob) {
	stagingPath := ""
	if blob.StagingPath != nil {
		stagingPath = *blob.StagingPath
	}
	s.abortBatch(ctx, []*StagedBlob{{
		ID:          blob.ID,
		StoragePath: blob.StoragePath,
		StagingPath: stagingPath,
	}})
}

func (s *BlobLifecycleService) abortBatch(ctx context.Context, stagedBlobs []*StagedBlob) {
	readyToRelease := make([]*StagedBlob, 0, len(stagedBlobs))
	cleanupErrors := make(map[string]error)
	cleanupBlobs := make(map[string]*StagedBlob)
	for _, staged := range stagedBlobs {
		if staged == nil || staged.ID == "" {
			continue
		}
		var deleteErr error
		if staged.StagingPath != "" {
			deleteErr = errors.Join(deleteErr, s.store.Delete(staged.StagingPath))
		}
		if staged.StoragePath != "" {
			deleteErr = errors.Join(deleteErr, s.store.Delete(staged.StoragePath))
		}
		if deleteErr == nil {
			readyToRelease = append(readyToRelease, staged)
			continue
		}
		cleanupBlobs[staged.ID] = staged
		cleanupErrors[staged.ID] = deleteErr
	}

	if len(readyToRelease) > 0 {
		ids := make([]string, 0, len(readyToRelease))
		for _, staged := range readyToRelease {
			ids = append(ids, staged.ID)
		}
		if err := s.blobRepo.ReleaseStagingBatch(ctx, ids, blobLedgerWriteBatchSize); err != nil {
			for _, staged := range readyToRelease {
				cleanupBlobs[staged.ID] = staged
				cleanupErrors[staged.ID] = err
			}
		}
	}

	if len(cleanupBlobs) == 0 {
		return
	}
	now := time.Now().UTC()
	jobs := make([]model.StorageCleanupJob, 0, len(cleanupBlobs))
	for blobID, staged := range cleanupBlobs {
		jobs = append(jobs, model.StorageCleanupJob{
			BlobID:      blobID,
			StoragePath: staged.StoragePath,
			NextRetryAt: now,
			LastError:   cleanupErrors[blobID].Error(),
		})
	}
	if err := s.blobRepo.EnqueueCleanupBatch(ctx, jobs, blobLedgerWriteBatchSize); err != nil {
		s.logger.Error(
			"track staging cleanup failed",
			zap.Int("blob_count", len(jobs)),
			zap.Error(err),
		)
		return
	}
	if s.wakeCleanup != nil {
		s.wakeCleanup()
	}
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
