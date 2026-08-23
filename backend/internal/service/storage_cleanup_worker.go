package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
)

const (
	defaultCleanupBatchSize = 50
	defaultCleanupLease     = 30 * time.Second
	defaultCleanupInterval  = 15 * time.Second
	defaultStagingTTL       = 24 * time.Hour
)

type StorageCleanupWorker struct {
	logger     *zap.Logger
	store      StorageCleanupStore
	blobRepo   *repository.StorageBlobRepository
	owner      string
	batchSize  int
	lease      time.Duration
	stagingTTL time.Duration
	wake       chan struct{}
	metrics    *RuntimeMetrics
}

type StorageCleanupStore interface {
	Delete(string) error
}

func (w *StorageCleanupWorker) SetMetrics(metrics *RuntimeMetrics) {
	w.metrics = metrics
}

func NewStorageCleanupWorker(
	logger *zap.Logger,
	store StorageCleanupStore,
	blobRepo *repository.StorageBlobRepository,
	stagingTTL time.Duration,
) *StorageCleanupWorker {
	if stagingTTL <= 0 {
		stagingTTL = defaultStagingTTL
	}

	return &StorageCleanupWorker{
		logger:     logger,
		store:      store,
		blobRepo:   blobRepo,
		owner:      uuid.NewString(),
		batchSize:  defaultCleanupBatchSize,
		lease:      defaultCleanupLease,
		stagingTTL: stagingTTL,
		wake:       make(chan struct{}, 1),
	}
}

func (w *StorageCleanupWorker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *StorageCleanupWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(defaultCleanupInterval)
	defer ticker.Stop()

	w.runAndLog(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runAndLog(ctx)
		case <-w.wake:
			w.runAndLog(ctx)
		}
	}
}

func (w *StorageCleanupWorker) RunOnce(ctx context.Context, now time.Time) error {
	cycleStartedAt := time.Now()
	leaseClock := func() time.Time {
		return now.Add(time.Since(cycleStartedAt))
	}
	defer func() {
		if backlog, err := w.blobRepo.CleanupJobCount(ctx); err == nil {
			w.metrics.SetCleanupBacklog(backlog)
		}
	}()

	if err := w.enqueueExpiredStaging(ctx, now); err != nil {
		return err
	}

	jobs, err := w.blobRepo.ListClaimCandidates(ctx, now, w.batchSize)
	if err != nil {
		return err
	}

	var joinedErr error
	for _, job := range jobs {
		claimNow := leaseClock()
		leaseOwner := w.owner + ":" + uuid.NewString()
		claimed, err := w.blobRepo.ClaimCleanupJob(ctx, job.ID, leaseOwner, claimNow, claimNow.Add(w.lease))
		if err != nil {
			joinedErr = errors.Join(joinedErr, err)
			continue
		}
		if !claimed {
			continue
		}

		if err := w.processClaimedJob(ctx, job.ID, leaseOwner, leaseClock); err != nil {
			joinedErr = errors.Join(joinedErr, err)
		}
	}

	return joinedErr
}

func (w *StorageCleanupWorker) RunOnceAtDatabaseTime(ctx context.Context) error {
	now, err := w.blobRepo.DatabaseTime(ctx)
	if err != nil {
		return err
	}
	return w.RunOnce(ctx, now)
}

func (w *StorageCleanupWorker) Backlog(ctx context.Context) (int64, error) {
	return w.blobRepo.CleanupJobCount(ctx)
}

func (w *StorageCleanupWorker) enqueueExpiredStaging(ctx context.Context, now time.Time) error {
	_, err := w.blobRepo.ExpireStagingForCleanup(ctx, now.Add(-w.stagingTTL), w.batchSize, now)
	return err
}

func (w *StorageCleanupWorker) processClaimedJob(
	ctx context.Context,
	jobID uint64,
	leaseOwner string,
	leaseClock func() time.Time,
) error {
	renewed, err := w.blobRepo.RenewCleanupJobLease(ctx, jobID, leaseOwner, leaseClock().Add(w.lease))
	if err != nil {
		return err
	}
	if !renewed {
		return fmt.Errorf("storage cleanup lease %d is no longer owned", jobID)
	}

	job, blob, err := w.loadClaimedJob(ctx, jobID, leaseOwner)
	if err != nil {
		return err
	}
	if blob.Status == model.StorageBlobStatusActive || blob.RefCount != 0 {
		err := fmt.Errorf("refusing to delete referenced blob %s", blob.ID)
		return w.failClaimedJob(ctx, *job, leaseOwner, leaseClock, err)
	}
	renewalCtx, stopRenewal := context.WithCancel(ctx)
	renewalDone := make(chan error, 1)
	go w.renewCleanupLease(renewalCtx, job.ID, leaseOwner, leaseClock, renewalDone)

	var deleteErr error
	if blob.StagingPath != nil && *blob.StagingPath != "" && *blob.StagingPath != blob.StoragePath {
		deleteErr = errors.Join(deleteErr, w.store.Delete(*blob.StagingPath))
	}
	deleteErr = errors.Join(deleteErr, w.store.Delete(blob.StoragePath))
	stopRenewal()
	renewalErr := <-renewalDone
	if deleteErr != nil {
		return errors.Join(renewalErr, w.failClaimedJob(ctx, *job, leaseOwner, leaseClock, deleteErr))
	}

	if err := w.blobRepo.CompleteCleanupJob(ctx, job.ID, leaseOwner); err != nil {
		w.metrics.RecordCleanup(false)
		return errors.Join(renewalErr, err)
	}
	w.metrics.RecordCleanup(true)
	if renewalErr != nil {
		w.logger.Warn("storage cleanup lease renewal failed before fenced completion", zap.Error(renewalErr))
	}

	w.logger.Info(
		"storage cleanup completed",
		zap.Uint64("cleanup_job_id", job.ID),
		zap.String("blob_id", blob.ID),
		zap.String("storage_path", blob.StoragePath),
	)
	return nil
}

func (w *StorageCleanupWorker) loadClaimedJob(
	ctx context.Context,
	jobID uint64,
	leaseOwner string,
) (*model.StorageCleanupJob, *model.StorageBlob, error) {
	claimed, err := w.blobRepo.FindClaimedCleanupJob(ctx, jobID, leaseOwner)
	if err != nil {
		return nil, nil, err
	}

	blob, err := w.blobRepo.FindByID(ctx, claimed.BlobID)
	if err != nil {
		return nil, nil, err
	}
	return claimed, blob, nil
}

func (w *StorageCleanupWorker) failClaimedJob(
	ctx context.Context,
	job model.StorageCleanupJob,
	leaseOwner string,
	leaseClock func() time.Time,
	cause error,
) error {
	w.metrics.RecordCleanup(false)
	nextRetryAt := leaseClock().Add(cleanupBackoff(job.RetryCount + 1))
	if err := w.blobRepo.FailCleanupJob(ctx, job.ID, leaseOwner, cause.Error(), nextRetryAt); err != nil {
		return errors.Join(cause, err)
	}

	w.logger.Warn(
		"storage cleanup failed",
		zap.Uint64("cleanup_job_id", job.ID),
		zap.String("blob_id", job.BlobID),
		zap.Uint("retry_count", job.RetryCount+1),
		zap.Time("next_retry_at", nextRetryAt),
		zap.Error(cause),
	)
	return cause
}

func (w *StorageCleanupWorker) renewCleanupLease(
	ctx context.Context,
	jobID uint64,
	leaseOwner string,
	leaseClock func() time.Time,
	done chan<- error,
) {
	interval := w.lease / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			renewed, err := w.blobRepo.RenewCleanupJobLease(
				ctx,
				jobID,
				leaseOwner,
				leaseClock().Add(w.lease),
			)
			if err != nil {
				done <- err
				return
			}
			if !renewed {
				done <- fmt.Errorf("storage cleanup lease %d is no longer owned", jobID)
				return
			}
		}
	}
}

func (w *StorageCleanupWorker) runAndLog(ctx context.Context) {
	if err := w.RunOnceAtDatabaseTime(ctx); err != nil && !errors.Is(err, context.Canceled) {
		w.logger.Error("storage cleanup cycle failed", zap.Error(err))
	}
}

func cleanupBackoff(retryCount uint) time.Duration {
	seconds := math.Pow(2, float64(min(retryCount, 12)))
	delay := time.Duration(seconds) * time.Second
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}
