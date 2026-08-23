package service

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/storage"
)

const (
	benchmarkBucketName      = "benchmark"
	benchmarkHistoryFileSize = 64
	benchmarkUploadFileSize  = 1024
)

var benchmarkUploadPayload = bytes.Repeat([]byte("x"), benchmarkUploadFileSize)

func TestClassifyUploadBenchmarkQuery(t *testing.T) {
	testCases := []struct {
		name  string
		query string
		want  uploadBenchmarkQueryClass
	}{
		{name: "object metadata", query: "SELECT * FROM `objects`", want: uploadBenchmarkQueryMetadata},
		{name: "bucket metadata", query: "SELECT count(*) FROM `buckets`", want: uploadBenchmarkQueryMetadata},
		{name: "blob ledger", query: "UPDATE `storage_blobs` SET `status` = ?", want: uploadBenchmarkQueryBlob},
		{name: "quota ledger", query: "UPDATE `system_storage_quotas` SET `used_bytes` = ?", want: uploadBenchmarkQueryBlob},
		{name: "transaction control", query: "SAVEPOINT sp0", want: uploadBenchmarkQueryOther},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := classifyUploadBenchmarkQuery(testCase.query); got != testCase.want {
				t.Fatalf("expected query class %d, got %d", testCase.want, got)
			}
		})
	}
}

func BenchmarkObjectUploadCurrentPath(b *testing.B) {
	for _, historySize := range []int{100, 10_000, 100_000} {
		b.Run(fmt.Sprintf("history_%d", historySize), func(b *testing.B) {
			b.Run("single", func(b *testing.B) {
				newUploadBenchmarkFixture(b, historySize).benchmarkSingleUpload(b)
			})
			b.Run("overwrite", func(b *testing.B) {
				newUploadBenchmarkFixture(b, historySize).benchmarkOverwriteUpload(b)
			})
			b.Run("batch_100", func(b *testing.B) {
				newUploadBenchmarkFixture(b, historySize).benchmarkBatchUpload(b, 100)
			})
			b.Run("batch_1000", func(b *testing.B) {
				newUploadBenchmarkFixture(b, historySize).benchmarkBatchUpload(b, 1000)
			})
		})
	}
}

type uploadBenchmarkFixture struct {
	db            *gorm.DB
	sqlDB         *sql.DB
	objectService *ObjectService
	objectRepo    *repository.ObjectRepository
	storage       *storage.LocalStorage
	metrics       *uploadBenchmarkMetrics
	logs          *observer.ObservedLogs
}

type uploadBenchmarkMetrics struct {
	enabled                  atomic.Bool
	queries                  atomic.Int64
	metadataQueries          atomic.Int64
	blobQueries              atomic.Int64
	otherQueries             atomic.Int64
	transactions             atomic.Int64
	metadataTransactions     atomic.Int64
	blobTransactions         atomic.Int64
	mixedTransactions        atomic.Int64
	otherTransactions        atomic.Int64
	transactionNanos         atomic.Int64
	metadataTransactionNanos atomic.Int64
	blobTransactionNanos     atomic.Int64
	mixedTransactionNanos    atomic.Int64
	otherTransactionNanos    atomic.Int64
	directoryScans           atomic.Int64
	directoryScanNanos       atomic.Int64
}

func newUploadBenchmarkFixture(b *testing.B, historySize int) *uploadBenchmarkFixture {
	b.Helper()
	b.StopTimer()

	root := b.TempDir()
	dsn := fmt.Sprintf("file:upload-benchmark-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		b.Fatalf("open benchmark sqlite database: %v", err)
	}
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		b.Fatalf("enable sqlite foreign keys: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Bucket{},
		&model.SystemStorageQuota{},
		&model.Object{},
		&model.RecycleBinObject{},
		&model.StorageBlob{},
		&model.StorageCleanupJob{},
	); err != nil {
		b.Fatalf("migrate benchmark sqlite database: %v", err)
	}

	storageRoot := filepath.Join(root, "storage")
	if err := os.MkdirAll(storageRoot, 0o755); err != nil {
		b.Fatalf("create benchmark storage root: %v", err)
	}
	seedUploadBenchmarkHistory(b, db, root, storageRoot, historySize)

	sqlDB, err := db.DB()
	if err != nil {
		b.Fatalf("open benchmark sql database: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	b.Cleanup(func() {
		_ = sqlDB.Close()
	})

	metrics := &uploadBenchmarkMetrics{}
	connPool := &uploadBenchmarkConnPool{db: sqlDB, metrics: metrics}
	db.Config.ConnPool = connPool
	db.Statement.ConnPool = connPool

	logCore, observedLogs := observer.New(zap.WarnLevel)
	benchmarkLogger := zap.New(logCore)
	bucketRepo := repository.NewBucketRepository(db)
	objectRepo := repository.NewObjectRepository(db)
	recycleRepo := repository.NewRecycleBinRepository(db)
	blobRepo := repository.NewStorageBlobRepository(db)
	localStorage := storage.NewLocalStorage(storageRoot)
	blobLifecycle := NewBlobLifecycleService(benchmarkLogger, db, localStorage, blobRepo, 0)
	objectService := NewObjectService(db, bucketRepo, objectRepo, recycleRepo, localStorage, blobLifecycle)

	originalDirectorySize := directorySize
	directorySize = func(root string) (uint64, error) {
		startedAt := time.Now()
		size, err := originalDirectorySize(root)
		if metrics.enabled.Load() {
			metrics.directoryScans.Add(1)
			metrics.directoryScanNanos.Add(time.Since(startedAt).Nanoseconds())
		}
		return size, err
	}
	b.Cleanup(func() {
		directorySize = originalDirectorySize
	})

	return &uploadBenchmarkFixture{
		db:            db,
		sqlDB:         sqlDB,
		objectService: objectService,
		objectRepo:    objectRepo,
		storage:       localStorage,
		metrics:       metrics,
		logs:          observedLogs,
	}
}

func seedUploadBenchmarkHistory(
	b *testing.B,
	db *gorm.DB,
	root string,
	storageRoot string,
	historySize int,
) {
	b.Helper()

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	reconciledAt := now
	if err := db.Create(&model.Bucket{Name: benchmarkBucketName, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		b.Fatalf("seed benchmark bucket: %v", err)
	}
	if err := db.Create(&model.SystemStorageQuota{
		ID:           1,
		MaxBytes:     defaultStorageQuotaMaxBytes,
		UsedBytes:    uint64(historySize * benchmarkHistoryFileSize),
		ReconciledAt: &reconciledAt,
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		b.Fatalf("seed benchmark quota: %v", err)
	}

	seedPath := filepath.Join(root, "history-seed.bin")
	seedContent := bytes.Repeat([]byte("h"), benchmarkHistoryFileSize)
	if err := os.WriteFile(seedPath, seedContent, 0o600); err != nil {
		b.Fatalf("write benchmark history seed: %v", err)
	}

	const batchSize = 500
	for start := 0; start < historySize; start += batchSize {
		end := start + batchSize
		if end > historySize {
			end = historySize
		}

		objects := make([]model.Object, 0, end-start)
		blobs := make([]model.StorageBlob, 0, end-start)
		for index := start; index < end; index++ {
			storagePath := filepath.ToSlash(filepath.Join(
				"history",
				fmt.Sprintf("%03d", index/1000),
				fmt.Sprintf("%06d.bin", index),
			))
			absolutePath := filepath.Join(storageRoot, filepath.FromSlash(storagePath))
			if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
				b.Fatalf("create benchmark history directory: %v", err)
			}
			if err := os.Link(seedPath, absolutePath); err != nil {
				if err := os.WriteFile(absolutePath, seedContent, 0o600); err != nil {
					b.Fatalf("write benchmark history object: %v", err)
				}
			}

			objectKey := fmt.Sprintf("history/%06d.bin", index)
			objects = append(objects, model.Object{
				BucketName:       benchmarkBucketName,
				ObjectKey:        objectKey,
				OriginalFilename: filepath.Base(objectKey),
				StoragePath:      storagePath,
				Size:             benchmarkHistoryFileSize,
				ContentType:      "application/octet-stream",
				ETag:             "benchmark-history",
				Visibility:       model.VisibilityPrivate,
				CreatedAt:        now,
				UpdatedAt:        now,
			})
			blobs = append(blobs, model.StorageBlob{
				ID:          fmt.Sprintf("benchmark-history-%010d", index),
				StoragePath: storagePath,
				Size:        benchmarkHistoryFileSize,
				RefCount:    1,
				Status:      model.StorageBlobStatusActive,
				CreatedAt:   now,
				UpdatedAt:   now,
			})
		}

		seedDB := db.Session(&gorm.Session{SkipDefaultTransaction: true})
		if err := seedDB.CreateInBatches(&objects, batchSize).Error; err != nil {
			b.Fatalf("seed benchmark object metadata: %v", err)
		}
		if err := seedDB.CreateInBatches(&blobs, batchSize).Error; err != nil {
			b.Fatalf("seed benchmark blob metadata: %v", err)
		}
	}
}

func (f *uploadBenchmarkFixture) benchmarkSingleUpload(b *testing.B) {
	ctx := context.Background()
	f.prepareMetrics(b)
	var writtenBytes int64
	var waitCount int64
	var waitDuration time.Duration
	requestDurations := make([]time.Duration, 0, b.N)

	for iteration := 0; iteration < b.N; iteration++ {
		objectKey := fmt.Sprintf("runs/single-%d.bin", iteration)
		beforeStats := f.sqlDB.Stats()

		f.metrics.enabled.Store(true)
		b.StartTimer()
		requestStartedAt := time.Now()
		object, err := f.objectService.Upload(ctx, UploadObjectInput{
			BucketName:       benchmarkBucketName,
			ObjectKey:        objectKey,
			Visibility:       "private",
			OriginalFilename: "single.bin",
			ContentType:      "application/octet-stream",
			Body:             bytes.NewReader(benchmarkUploadPayload),
		})
		requestDurations = append(requestDurations, time.Since(requestStartedAt))
		b.StopTimer()
		f.metrics.enabled.Store(false)
		if err != nil {
			b.Fatalf("upload single object: %v", err)
		}

		afterStats := f.sqlDB.Stats()
		waitCount += afterStats.WaitCount - beforeStats.WaitCount
		waitDuration += afterStats.WaitDuration - beforeStats.WaitDuration
		writtenBytes += object.Size
		f.deleteBenchmarkObjects(b, ctx, []model.Object{*object})
	}

	f.reportMetrics(b, writtenBytes, waitCount, waitDuration, 1)
	reportUploadLatencyPercentiles(b, requestDurations)
}

func (f *uploadBenchmarkFixture) benchmarkOverwriteUpload(b *testing.B) {
	ctx := context.Background()
	f.prepareMetrics(b)
	var writtenBytes int64
	var waitCount int64
	var waitDuration time.Duration
	requestDurations := make([]time.Duration, 0, b.N)

	for iteration := 0; iteration < b.N; iteration++ {
		beforeStats := f.sqlDB.Stats()

		f.metrics.enabled.Store(true)
		b.StartTimer()
		requestStartedAt := time.Now()
		object, err := f.objectService.Upload(ctx, UploadObjectInput{
			BucketName:       benchmarkBucketName,
			ObjectKey:        "history/000000.bin",
			Visibility:       "private",
			AllowOverwrite:   true,
			OriginalFilename: "overwrite.bin",
			ContentType:      "application/octet-stream",
			Body:             bytes.NewReader(benchmarkUploadPayload),
		})
		requestDurations = append(requestDurations, time.Since(requestStartedAt))
		b.StopTimer()
		f.metrics.enabled.Store(false)
		if err != nil {
			b.Fatalf("overwrite object: %v", err)
		}

		afterStats := f.sqlDB.Stats()
		waitCount += afterStats.WaitCount - beforeStats.WaitCount
		waitDuration += afterStats.WaitDuration - beforeStats.WaitDuration
		writtenBytes += object.Size
	}

	f.reportMetrics(b, writtenBytes, waitCount, waitDuration, 1)
	reportUploadLatencyPercentiles(b, requestDurations)
}

func (f *uploadBenchmarkFixture) benchmarkBatchUpload(b *testing.B, fileCount int) {
	ctx := context.Background()
	items := make([]UploadObjectBatchItemInput, 0, fileCount)
	fileSize := uint64(len(benchmarkUploadPayload))
	for index := 0; index < fileCount; index++ {
		items = append(items, UploadObjectBatchItemInput{
			RelativePath:     fmt.Sprintf("%04d.bin", index),
			OriginalFilename: fmt.Sprintf("%04d.bin", index),
			ContentType:      "application/octet-stream",
			Size:             &fileSize,
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(benchmarkUploadPayload)), nil
			},
		})
	}

	f.prepareMetrics(b)
	var writtenBytes int64
	var waitCount int64
	var waitDuration time.Duration
	requestDurations := make([]time.Duration, 0, b.N)
	for iteration := 0; iteration < b.N; iteration++ {
		prefix := fmt.Sprintf("runs/batch-%d/", iteration)
		beforeStats := f.sqlDB.Stats()

		f.metrics.enabled.Store(true)
		b.StartTimer()
		requestStartedAt := time.Now()
		output, err := f.objectService.UploadBatch(ctx, UploadObjectBatchInput{
			BucketName: benchmarkBucketName,
			Prefix:     prefix,
			Visibility: "private",
			Items:      items,
		})
		requestDurations = append(requestDurations, time.Since(requestStartedAt))
		b.StopTimer()
		f.metrics.enabled.Store(false)
		if err != nil {
			b.Fatalf("upload %d object batch: %v", fileCount, err)
		}

		afterStats := f.sqlDB.Stats()
		waitCount += afterStats.WaitCount - beforeStats.WaitCount
		waitDuration += afterStats.WaitDuration - beforeStats.WaitDuration
		for index := range output.Items {
			writtenBytes += output.Items[index].Size
		}
		f.deleteBenchmarkObjects(b, ctx, output.Items)
	}

	f.reportMetrics(b, writtenBytes, waitCount, waitDuration, fileCount)
	reportUploadLatencyPercentiles(b, requestDurations)
	f.assertBatchDatabaseBudget(b, fileCount)
}

func reportUploadLatencyPercentiles(b *testing.B, durations []time.Duration) {
	b.Helper()
	if len(durations) == 0 {
		return
	}

	// The first request warms the database and filesystem caches. A single-iteration
	// smoke benchmark still reports that request so existing commands remain useful.
	warmDurations := durations
	if len(warmDurations) > 1 {
		warmDurations = warmDurations[1:]
	}
	sorted := append([]time.Duration(nil), warmDurations...)
	slices.Sort(sorted)
	b.ReportMetric(float64(sorted[(len(sorted)-1)/2].Nanoseconds()), "upload_p50_ns")
	p95Index := (95*len(sorted) + 99) / 100
	b.ReportMetric(float64(sorted[p95Index-1].Nanoseconds()), "upload_p95_ns")
}

func (f *uploadBenchmarkFixture) assertBatchDatabaseBudget(b *testing.B, fileCount int) {
	b.Helper()
	operations := int64(b.N)
	batchesPerOperation := int64((fileCount + blobLedgerWriteBatchSize - 1) / blobLedgerWriteBatchSize)
	// Each blob batch now includes one database-clock staging-lease renewal in
	// addition to reservation, ledger creation, and activation.
	maxBlobQueries := operations * (4*batchesPerOperation + 2)
	if got := f.metrics.blobQueries.Load(); got > maxBlobQueries {
		b.Fatalf("blob queries = %d, want at most %d for %d operations", got, maxBlobQueries, operations)
	}
	maxQueries := operations * (8*batchesPerOperation + 4)
	if got := f.metrics.queries.Load(); got > maxQueries {
		b.Fatalf("total queries = %d, want at most %d for %d operations", got, maxQueries, operations)
	}
	maxTransactions := operations * 2
	if got := f.metrics.transactions.Load(); got > maxTransactions {
		b.Fatalf("transactions = %d, want at most %d for %d operations", got, maxTransactions, operations)
	}
	if got := f.metrics.directoryScans.Load(); got != 0 {
		b.Fatalf("directory scans = %d, want 0", got)
	}
}

func (f *uploadBenchmarkFixture) prepareMetrics(b *testing.B) {
	b.Helper()
	b.ReportAllocs()
	b.StopTimer()
	f.metrics.reset()
	_ = f.logs.TakeAll()
	b.ResetTimer()
}

func (f *uploadBenchmarkFixture) reportMetrics(
	b *testing.B,
	writtenBytes int64,
	waitCount int64,
	waitDuration time.Duration,
	filesPerOperation int,
) {
	b.Helper()
	operations := float64(b.N)
	files := operations * float64(filesPerOperation)
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/files, "upload_ns/file")
	b.ReportMetric(float64(f.metrics.queries.Load())/operations, "queries/op")
	b.ReportMetric(float64(f.metrics.metadataQueries.Load())/operations, "metadata_queries/op")
	b.ReportMetric(float64(f.metrics.blobQueries.Load())/operations, "blob_queries/op")
	b.ReportMetric(float64(f.metrics.otherQueries.Load())/operations, "other_queries/op")
	b.ReportMetric(float64(f.metrics.transactions.Load())/operations, "transactions/op")
	b.ReportMetric(float64(f.metrics.metadataTransactions.Load())/operations, "metadata_transactions/op")
	b.ReportMetric(float64(f.metrics.blobTransactions.Load())/operations, "blob_transactions/op")
	b.ReportMetric(float64(f.metrics.mixedTransactions.Load())/operations, "mixed_transactions/op")
	b.ReportMetric(float64(f.metrics.otherTransactions.Load())/operations, "other_transactions/op")
	b.ReportMetric(float64(f.metrics.transactionNanos.Load())/operations, "transaction_ns/op")
	b.ReportMetric(float64(f.metrics.metadataTransactionNanos.Load())/operations, "metadata_transaction_ns/op")
	b.ReportMetric(float64(f.metrics.blobTransactionNanos.Load())/operations, "blob_transaction_ns/op")
	b.ReportMetric(float64(f.metrics.mixedTransactionNanos.Load())/operations, "mixed_transaction_ns/op")
	b.ReportMetric(float64(f.metrics.otherTransactionNanos.Load())/operations, "other_transaction_ns/op")
	b.ReportMetric(float64(f.metrics.directoryScans.Load())/operations, "directory_scans/op")
	b.ReportMetric(float64(f.metrics.directoryScanNanos.Load())/operations, "directory_scan_ns/op")
	b.ReportMetric(float64(writtenBytes)/operations, "written_bytes/op")
	b.ReportMetric(float64(waitCount)/operations, "db_waits/op")
	b.ReportMetric(float64(waitDuration.Nanoseconds())/operations, "db_wait_ns/op")
	b.ReportMetric(float64(countCleanupFailureLogs(f.logs.TakeAll()))/operations, "cleanup_failures/op")
}

func (f *uploadBenchmarkFixture) deleteBenchmarkObjects(
	b *testing.B,
	ctx context.Context,
	objects []model.Object,
) {
	b.Helper()

	for index := range objects {
		object := objects[index]
		if _, err := f.objectRepo.HardDelete(ctx, object.BucketName, object.ObjectKey); err != nil {
			b.Fatalf("delete benchmark object metadata: %v", err)
		}
		if err := f.storage.Delete(object.StoragePath); err != nil {
			b.Fatalf("delete benchmark object file: %v", err)
		}
	}
}

func (m *uploadBenchmarkMetrics) reset() {
	m.enabled.Store(false)
	m.queries.Store(0)
	m.metadataQueries.Store(0)
	m.blobQueries.Store(0)
	m.otherQueries.Store(0)
	m.transactions.Store(0)
	m.metadataTransactions.Store(0)
	m.blobTransactions.Store(0)
	m.mixedTransactions.Store(0)
	m.otherTransactions.Store(0)
	m.transactionNanos.Store(0)
	m.metadataTransactionNanos.Store(0)
	m.blobTransactionNanos.Store(0)
	m.mixedTransactionNanos.Store(0)
	m.otherTransactionNanos.Store(0)
	m.directoryScans.Store(0)
	m.directoryScanNanos.Store(0)
}

func countCleanupFailureLogs(entries []observer.LoggedEntry) int {
	count := 0
	for _, entry := range entries {
		switch entry.Message {
		case "delete storage path failed", "cleanup unreferenced storage path failed", "track staging cleanup failed":
			count++
		}
	}
	return count
}

type uploadBenchmarkConnPool struct {
	db      *sql.DB
	metrics *uploadBenchmarkMetrics
}

func (p *uploadBenchmarkConnPool) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return p.db.PrepareContext(ctx, query)
}

func (p *uploadBenchmarkConnPool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	p.recordQuery(query)
	return p.db.ExecContext(ctx, query, args...)
}

func (p *uploadBenchmarkConnPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	p.recordQuery(query)
	return p.db.QueryContext(ctx, query, args...)
}

func (p *uploadBenchmarkConnPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	p.recordQuery(query)
	return p.db.QueryRowContext(ctx, query, args...)
}

func (p *uploadBenchmarkConnPool) BeginTx(
	ctx context.Context,
	opts *sql.TxOptions,
) (gorm.ConnPool, error) {
	startedAt := time.Now()
	tx, err := p.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}

	return &uploadBenchmarkTx{
		Tx:        tx,
		metrics:   p.metrics,
		startedAt: startedAt,
		measured:  p.metrics.enabled.Load(),
	}, nil
}

func (p *uploadBenchmarkConnPool) recordQuery(query string) {
	if p.metrics.enabled.Load() {
		p.metrics.recordQuery(classifyUploadBenchmarkQuery(query))
	}
}

type uploadBenchmarkTx struct {
	*sql.Tx
	metrics   *uploadBenchmarkMetrics
	startedAt time.Time
	measured  bool
	finished  atomic.Bool
	metadata  bool
	blob      bool
	other     bool
}

func (tx *uploadBenchmarkTx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return tx.Tx.PrepareContext(ctx, query)
}

func (tx *uploadBenchmarkTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	tx.recordQuery(query)
	return tx.Tx.ExecContext(ctx, query, args...)
}

func (tx *uploadBenchmarkTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	tx.recordQuery(query)
	return tx.Tx.QueryContext(ctx, query, args...)
}

func (tx *uploadBenchmarkTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	tx.recordQuery(query)
	return tx.Tx.QueryRowContext(ctx, query, args...)
}

func (tx *uploadBenchmarkTx) Commit() error {
	err := tx.Tx.Commit()
	tx.recordTransaction()
	return err
}

func (tx *uploadBenchmarkTx) Rollback() error {
	err := tx.Tx.Rollback()
	tx.recordTransaction()
	return err
}

func (tx *uploadBenchmarkTx) recordQuery(query string) {
	if tx.metrics.enabled.Load() {
		queryClass := classifyUploadBenchmarkQuery(query)
		tx.metrics.recordQuery(queryClass)
		switch queryClass {
		case uploadBenchmarkQueryMetadata:
			tx.metadata = true
		case uploadBenchmarkQueryBlob:
			tx.blob = true
		default:
			tx.other = true
		}
	}
}

func (tx *uploadBenchmarkTx) recordTransaction() {
	if !tx.measured || !tx.finished.CompareAndSwap(false, true) {
		return
	}

	durationNanos := time.Since(tx.startedAt).Nanoseconds()
	tx.metrics.transactions.Add(1)
	tx.metrics.transactionNanos.Add(durationNanos)
	switch {
	case tx.metadata && tx.blob:
		tx.metrics.mixedTransactions.Add(1)
		tx.metrics.mixedTransactionNanos.Add(durationNanos)
	case tx.blob:
		tx.metrics.blobTransactions.Add(1)
		tx.metrics.blobTransactionNanos.Add(durationNanos)
	case tx.metadata:
		tx.metrics.metadataTransactions.Add(1)
		tx.metrics.metadataTransactionNanos.Add(durationNanos)
	default:
		tx.metrics.otherTransactions.Add(1)
		tx.metrics.otherTransactionNanos.Add(durationNanos)
	}
}

type uploadBenchmarkQueryClass uint8

const (
	uploadBenchmarkQueryOther uploadBenchmarkQueryClass = iota
	uploadBenchmarkQueryMetadata
	uploadBenchmarkQueryBlob
)

func classifyUploadBenchmarkQuery(query string) uploadBenchmarkQueryClass {
	normalized := strings.ToLower(query)
	switch {
	case strings.Contains(normalized, "storage_blobs"),
		strings.Contains(normalized, "system_storage_quotas"),
		strings.Contains(normalized, "storage_cleanup_jobs"):
		return uploadBenchmarkQueryBlob
	case strings.Contains(normalized, "objects"), strings.Contains(normalized, "buckets"):
		return uploadBenchmarkQueryMetadata
	default:
		return uploadBenchmarkQueryOther
	}
}

func (m *uploadBenchmarkMetrics) recordQuery(queryClass uploadBenchmarkQueryClass) {
	m.queries.Add(1)
	switch queryClass {
	case uploadBenchmarkQueryMetadata:
		m.metadataQueries.Add(1)
	case uploadBenchmarkQueryBlob:
		m.blobQueries.Add(1)
	default:
		m.otherQueries.Add(1)
	}
}
