package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/service"
	"light-oss/backend/internal/storage"
)

const (
	mysqlUploadBenchmarkBucket       = "mysql-upload-benchmark"
	mysqlUploadBenchmarkHistoryBytes = 64
	mysqlUploadBenchmarkPayloadBytes = 1024
	mysqlUploadBenchmarkSeedBatch    = 500
	mysqlUploadBenchmarkWarmups      = 5
)

var mysqlUploadBenchmarkPayload = bytes.Repeat([]byte("x"), mysqlUploadBenchmarkPayloadBytes)

func BenchmarkMySQLObjectUploadLatency(b *testing.B) {
	fixtures := make([]*mysqlUploadBenchmarkFixture, 0, 2)
	for _, historySize := range []int{100, 100_000} {
		fixtures = append(fixtures, newMySQLUploadBenchmarkFixture(b, historySize))
	}

	ctx := context.Background()
	for _, fixture := range fixtures {
		for warmup := 0; warmup < mysqlUploadBenchmarkWarmups; warmup++ {
			if _, err := fixture.upload(ctx, fmt.Sprintf("runs/warmup-%06d.bin", warmup)); err != nil {
				b.Fatalf("warm history %d upload path: %v", fixture.historySize, err)
			}
		}
	}

	durations := make(map[int][]time.Duration, len(fixtures))
	databasePools := make(map[int]*sql.DB, len(fixtures))
	beforeStats := make(map[int]sql.DBStats, len(fixtures))
	for _, fixture := range fixtures {
		durations[fixture.historySize] = make([]time.Duration, 0, b.N)
		databasePool, err := fixture.db.DB()
		if err != nil {
			b.Fatalf("load history %d sql.DB: %v", fixture.historySize, err)
		}
		databasePools[fixture.historySize] = databasePool
		beforeStats[fixture.historySize] = databasePool.Stats()
		fixture.sqlQueries.queries.Store(0)
		fixture.sqlQueries.enabled.Store(true)
	}

	b.SetBytes(mysqlUploadBenchmarkPayloadBytes * int64(len(fixtures)))
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		for fixtureIndex := range fixtures {
			// Reverse each iteration so host load and MySQL background work affect
			// both history sizes evenly instead of biasing the second sub-benchmark.
			if iteration%2 != 0 {
				fixtureIndex = len(fixtures) - 1 - fixtureIndex
			}
			fixture := fixtures[fixtureIndex]
			startedAt := time.Now()
			if _, err := fixture.upload(ctx, fmt.Sprintf("runs/measured-%06d.bin", iteration)); err != nil {
				b.Fatalf("upload history %d measured object %d: %v", fixture.historySize, iteration, err)
			}
			durations[fixture.historySize] = append(durations[fixture.historySize], time.Since(startedAt))
		}
	}
	b.StopTimer()

	p95ByHistory := make(map[int]time.Duration, len(fixtures))
	for _, fixture := range fixtures {
		fixture.sqlQueries.enabled.Store(false)
		historyDurations := durations[fixture.historySize]
		p50, p95 := mysqlUploadBenchmarkPercentiles(historyDurations)
		p95ByHistory[fixture.historySize] = p95
		afterStats := databasePools[fixture.historySize].Stats()
		initialStats := beforeStats[fixture.historySize]
		metricPrefix := fmt.Sprintf("history_%d_", fixture.historySize)
		b.ReportMetric(float64(p50.Nanoseconds()), metricPrefix+"upload_p50_ns")
		b.ReportMetric(float64(p95.Nanoseconds()), metricPrefix+"upload_p95_ns")
		b.ReportMetric(float64(fixture.sqlQueries.queries.Load())/float64(b.N), metricPrefix+"queries/op")
		b.ReportMetric(float64(afterStats.WaitCount-initialStats.WaitCount)/float64(b.N), metricPrefix+"db_waits/op")
		b.ReportMetric(float64((afterStats.WaitDuration-initialStats.WaitDuration).Nanoseconds())/float64(b.N), metricPrefix+"db_wait_ns/op")
		b.Logf(
			"mysql_version=%s history=%d samples=%d warmups=%d upload_p50_ns=%d upload_p95_ns=%d",
			fixture.mysqlVersion,
			fixture.historySize,
			len(historyDurations),
			mysqlUploadBenchmarkWarmups,
			p50.Nanoseconds(),
			p95.Nanoseconds(),
		)
	}

	p95Increase := (float64(p95ByHistory[100_000]) - float64(p95ByHistory[100])) / float64(p95ByHistory[100]) * 100
	b.ReportMetric(p95Increase, "history_p95_increase_pct")
	b.Logf("history_100_to_100000_p95_increase_pct=%.2f threshold_pct=20.00 passed=%t", p95Increase, p95Increase <= 20)
	if b.N >= 20 && p95Increase > 20 {
		b.Fatalf("history-size p95 increase %.2f%% exceeds 20%% threshold", p95Increase)
	}
}

type mysqlUploadBenchmarkFixture struct {
	objects      *service.ObjectService
	db           *gorm.DB
	sqlQueries   *mysqlUploadBenchmarkQueryLogger
	mysqlVersion string
	historySize  int
}

func newMySQLUploadBenchmarkFixture(b *testing.B, historySize int) *mysqlUploadBenchmarkFixture {
	b.Helper()
	b.StopTimer()

	dsn := newIsolatedMySQLDatabase(b)
	migrator := newMigrator(b, dsn)
	if err := migrator.Up(); err != nil {
		b.Fatalf("migration up: %v", err)
	}

	db := openGorm(b, dsn)
	queryLogger := &mysqlUploadBenchmarkQueryLogger{Interface: logger.Discard}
	db.Config.Logger = queryLogger
	sqlDB, err := db.DB()
	if err != nil {
		b.Fatalf("load benchmark sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	seedMySQLUploadBenchmarkHistory(b, db, historySize)
	storageRoot := b.TempDir()
	localStorage := storage.NewLocalStorage(storageRoot)
	blobRepo := repository.NewStorageBlobRepository(db)
	blobLifecycle := service.NewBlobLifecycleService(zap.NewNop(), db, localStorage, blobRepo, 0)
	objectService := service.NewObjectService(
		db,
		repository.NewBucketRepository(db),
		repository.NewObjectRepository(db),
		repository.NewRecycleBinRepository(db),
		localStorage,
		blobLifecycle,
	)

	var mysqlVersion string
	if err := db.Raw("SELECT VERSION()").Scan(&mysqlVersion).Error; err != nil {
		b.Fatalf("read MySQL version: %v", err)
	}

	return &mysqlUploadBenchmarkFixture{
		objects:      objectService,
		db:           db,
		sqlQueries:   queryLogger,
		mysqlVersion: mysqlVersion,
		historySize:  historySize,
	}
}

func seedMySQLUploadBenchmarkHistory(b *testing.B, db *gorm.DB, historySize int) {
	b.Helper()
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Create(&model.Bucket{
		Name:      mysqlUploadBenchmarkBucket,
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		b.Fatalf("seed benchmark bucket: %v", err)
	}

	seedDB := db.Session(&gorm.Session{SkipDefaultTransaction: true})
	for start := 0; start < historySize; start += mysqlUploadBenchmarkSeedBatch {
		end := min(start+mysqlUploadBenchmarkSeedBatch, historySize)
		objects := make([]model.Object, 0, end-start)
		blobs := make([]model.StorageBlob, 0, end-start)
		for index := start; index < end; index++ {
			storagePath := fmt.Sprintf("objects/history/%06d.bin", index)
			objectKey := fmt.Sprintf("history/%06d.bin", index)
			objects = append(objects, model.Object{
				BucketName:       mysqlUploadBenchmarkBucket,
				ObjectKey:        objectKey,
				OriginalFilename: fmt.Sprintf("%06d.bin", index),
				StoragePath:      storagePath,
				Size:             mysqlUploadBenchmarkHistoryBytes,
				ContentType:      "application/octet-stream",
				ETag:             "mysql-upload-benchmark-history",
				Visibility:       model.VisibilityPrivate,
				CreatedAt:        now,
				UpdatedAt:        now,
			})
			blobs = append(blobs, model.StorageBlob{
				ID:          fmt.Sprintf("history-%028d", index),
				StoragePath: storagePath,
				Size:        mysqlUploadBenchmarkHistoryBytes,
				RefCount:    1,
				Status:      model.StorageBlobStatusActive,
				CreatedAt:   now,
				UpdatedAt:   now,
			})
		}

		if err := seedDB.Create(&objects).Error; err != nil {
			b.Fatalf("seed benchmark objects %d..%d: %v", start, end, err)
		}
		if err := seedDB.Create(&blobs).Error; err != nil {
			b.Fatalf("seed benchmark blobs %d..%d: %v", start, end, err)
		}
	}

	reconciledAt := now
	result := db.Model(&model.SystemStorageQuota{}).
		Where("id = ?", 1).
		Updates(map[string]any{
			"max_bytes":      uint64(1) << 40,
			"used_bytes":     uint64(historySize * mysqlUploadBenchmarkHistoryBytes),
			"reserved_bytes": 0,
			"reconciled_at":  reconciledAt,
		})
	if result.Error != nil {
		b.Fatalf("seed benchmark quota: %v", result.Error)
	}
	if result.RowsAffected != 1 {
		b.Fatalf("seed benchmark quota: affected rows = %d, want 1", result.RowsAffected)
	}
}

func (f *mysqlUploadBenchmarkFixture) upload(ctx context.Context, objectKey string) (*model.Object, error) {
	return f.objects.Upload(ctx, service.UploadObjectInput{
		BucketName:       mysqlUploadBenchmarkBucket,
		ObjectKey:        objectKey,
		Visibility:       string(model.VisibilityPrivate),
		OriginalFilename: "upload.bin",
		ContentType:      "application/octet-stream",
		Body:             bytes.NewReader(mysqlUploadBenchmarkPayload),
	})
}

func mysqlUploadBenchmarkPercentiles(durations []time.Duration) (time.Duration, time.Duration) {
	sorted := append([]time.Duration(nil), durations...)
	slices.Sort(sorted)
	p50 := sorted[(len(sorted)-1)/2]
	p95Index := (95*len(sorted) + 99) / 100
	return p50, sorted[p95Index-1]
}

type mysqlUploadBenchmarkQueryLogger struct {
	logger.Interface
	enabled atomic.Bool
	queries atomic.Int64
}

func (l *mysqlUploadBenchmarkQueryLogger) Trace(
	ctx context.Context,
	begin time.Time,
	query func() (string, int64),
	err error,
) {
	if l.enabled.Load() {
		l.queries.Add(1)
	}
	l.Interface.Trace(ctx, begin, query, err)
}
