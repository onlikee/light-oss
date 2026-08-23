package service

import (
	"sync/atomic"
	"time"
)

type RuntimeMetrics struct {
	uploadsStaged              atomic.Uint64
	uploadsFailed              atomic.Uint64
	uploadBytes                atomic.Uint64
	uploadStagingDurationNanos atomic.Int64
	reservationFailures        atomic.Uint64
	transactionsCompleted      atomic.Uint64
	transactionsFailed         atomic.Uint64
	transactionDurationNanos   atomic.Int64
	cleanupCompleted           atomic.Uint64
	cleanupFailed              atomic.Uint64
	cleanupBacklog             atomic.Int64
}

type RuntimeMetricsSnapshot struct {
	UploadsStaged              uint64
	UploadsFailed              uint64
	UploadBytes                uint64
	UploadStagingDurationNanos int64
	ReservationFailures        uint64
	TransactionsCompleted      uint64
	TransactionsFailed         uint64
	TransactionDurationNanos   int64
	CleanupCompleted           uint64
	CleanupFailed              uint64
	CleanupBacklog             int64
}

func (m *RuntimeMetrics) RecordUploadStaging(size uint64, duration time.Duration, err error) {
	if m == nil {
		return
	}
	m.uploadStagingDurationNanos.Add(duration.Nanoseconds())
	if err != nil {
		m.uploadsFailed.Add(1)
		return
	}
	m.uploadsStaged.Add(1)
	m.uploadBytes.Add(size)
}

func (m *RuntimeMetrics) RecordReservationFailure() {
	if m != nil {
		m.reservationFailures.Add(1)
	}
}

func (m *RuntimeMetrics) RecordTransaction(duration time.Duration, err error) {
	if m == nil {
		return
	}
	m.transactionDurationNanos.Add(duration.Nanoseconds())
	if err != nil {
		m.transactionsFailed.Add(1)
		return
	}
	m.transactionsCompleted.Add(1)
}

func (m *RuntimeMetrics) RecordCleanup(completed bool) {
	if m == nil {
		return
	}
	if completed {
		m.cleanupCompleted.Add(1)
		return
	}
	m.cleanupFailed.Add(1)
}

func (m *RuntimeMetrics) SetCleanupBacklog(backlog int64) {
	if m != nil {
		m.cleanupBacklog.Store(backlog)
	}
}

func (m *RuntimeMetrics) Snapshot() RuntimeMetricsSnapshot {
	if m == nil {
		return RuntimeMetricsSnapshot{}
	}
	return RuntimeMetricsSnapshot{
		UploadsStaged:              m.uploadsStaged.Load(),
		UploadsFailed:              m.uploadsFailed.Load(),
		UploadBytes:                m.uploadBytes.Load(),
		UploadStagingDurationNanos: m.uploadStagingDurationNanos.Load(),
		ReservationFailures:        m.reservationFailures.Load(),
		TransactionsCompleted:      m.transactionsCompleted.Load(),
		TransactionsFailed:         m.transactionsFailed.Load(),
		TransactionDurationNanos:   m.transactionDurationNanos.Load(),
		CleanupCompleted:           m.cleanupCompleted.Load(),
		CleanupFailed:              m.cleanupFailed.Load(),
		CleanupBacklog:             m.cleanupBacklog.Load(),
	}
}
