package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	"gorm.io/gorm"

	"light-oss/backend/internal/config"
	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/service"
	"light-oss/backend/internal/storage"
)

type stubMigrationRunner struct {
	err   error
	calls int
}

type stubReconciliationStateReader struct {
	quota *model.SystemStorageQuota
	err   error
}

func (s *stubReconciliationStateReader) Get(context.Context) (*model.SystemStorageQuota, error) {
	return s.quota, s.err
}

type stubStorageReconciliationRunner struct {
	report *service.StorageReconciliationReport
	err    error
	calls  int
}

func (s *stubStorageReconciliationRunner) Reconcile(context.Context) (*service.StorageReconciliationReport, error) {
	s.calls++
	return s.report, s.err
}

func TestDatabaseDSNWithTimeouts(t *testing.T) {
	cfg := config.Config{
		DatabaseDSN:                   "test-user:test-password@tcp(127.0.0.1:3306)/light_oss?parseTime=true&loc=UTC",
		DatabaseConnectTimeoutSeconds: 4,
		DatabaseReadTimeoutSeconds:    120,
		DatabaseWriteTimeoutSeconds:   15,
	}
	normalized, err := databaseDSNWithTimeouts(cfg)
	if err != nil {
		t.Fatalf("databaseDSNWithTimeouts() error = %v", err)
	}
	parsed, err := mysql.ParseDSN(normalized)
	if err != nil {
		t.Fatalf("parse normalized DSN: %v", err)
	}
	if parsed.Timeout != 4*time.Second || parsed.ReadTimeout != 120*time.Second || parsed.WriteTimeout != 15*time.Second {
		t.Fatalf(
			"timeouts = connect:%s read:%s write:%s",
			parsed.Timeout,
			parsed.ReadTimeout,
			parsed.WriteTimeout,
		)
	}
	if parsed.User != "test-user" || parsed.DBName != "light_oss" || !parsed.ParseTime {
		t.Fatalf("normalized DSN lost existing settings: %+v", parsed)
	}
}

func (r *stubMigrationRunner) Up() error {
	r.calls++
	return r.err
}

func TestApplyMigrations(t *testing.T) {
	genericErr := errors.New("migration failed")
	tests := []struct {
		name         string
		migrationErr error
		wantErr      error
		wantMessage  string
	}{
		{name: "success"},
		{name: "no change", migrationErr: migrate.ErrNoChange},
		{name: "generic failure", migrationErr: genericErr, wantErr: genericErr},
		{
			name:         "dirty state fails fast",
			migrationErr: migrate.ErrDirty{Version: 6},
			wantErr:      migrate.ErrDirty{Version: 6},
			wantMessage:  "version 6 is dirty; repair or roll back the failed migration and resolve schema_migrations manually before restarting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &stubMigrationRunner{err: tt.migrationErr}
			err := applyMigrations(runner)

			if tt.wantErr == nil && err != nil {
				t.Fatalf("applyMigrations() error = %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("applyMigrations() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantMessage != "" && !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("applyMigrations() error = %q, want message containing %q", err, tt.wantMessage)
			}
			if runner.calls != 1 {
				t.Fatalf("Up() calls = %d, want 1", runner.calls)
			}
		})
	}
}

func TestCheckRuntimeReadiness(t *testing.T) {
	gormDB, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gormDB.AutoMigrate(&model.SystemStorageQuota{}); err != nil {
		t.Fatalf("migrate quota: %v", err)
	}
	if err := gormDB.Exec("CREATE TABLE schema_migrations (version INTEGER NOT NULL, dirty BOOLEAN NOT NULL)").Error; err != nil {
		t.Fatalf("create migration state: %v", err)
	}
	if err := gormDB.Exec("INSERT INTO schema_migrations (version, dirty) VALUES (?, ?)", 7, false).Error; err != nil {
		t.Fatalf("insert migration state: %v", err)
	}

	quotaRepo := repository.NewStorageQuotaRepository(gormDB)
	if _, err := quotaRepo.EnsureDefault(context.Background(), 1024); err != nil {
		t.Fatalf("initialize quota: %v", err)
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	localStorage := storage.NewLocalStorage(t.TempDir())

	if err := checkRuntimeReadiness(context.Background(), sqlDB, localStorage, localStorage, quotaRepo); err == nil || !strings.Contains(err.Error(), "reconciliation") {
		t.Fatalf("expected reconciliation readiness failure, got %v", err)
	}

	now := time.Now().UTC()
	if err := gormDB.Model(&model.SystemStorageQuota{}).Where("id = ?", 1).Update("reconciled_at", now).Error; err != nil {
		t.Fatalf("mark reconciled: %v", err)
	}
	storageID, err := localStorage.Identity(context.Background())
	if err != nil {
		t.Fatalf("read storage identity: %v", err)
	}
	if err := quotaRepo.BindStorageIdentity(context.Background(), storageID); err != nil {
		t.Fatalf("bind storage identity: %v", err)
	}
	if err := checkRuntimeReadiness(context.Background(), sqlDB, localStorage, localStorage, quotaRepo); err != nil {
		t.Fatalf("expected ready state, got %v", err)
	}

	if err := gormDB.Exec("UPDATE schema_migrations SET dirty = ?", true).Error; err != nil {
		t.Fatalf("mark migration dirty: %v", err)
	}
	if err := checkRuntimeReadiness(context.Background(), sqlDB, localStorage, localStorage, quotaRepo); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("expected dirty migration failure, got %v", err)
	}
}

func TestReconcileStorageIfNeededDoesNotRescanCompletedSharedState(t *testing.T) {
	now := time.Now().UTC()
	storageID := "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name       string
		force      bool
		reconciled *time.Time
		wantRan    bool
	}{
		{name: "completed normal startup", reconciled: &now, wantRan: false},
		{name: "first startup", reconciled: nil, wantRan: true},
		{name: "explicit reconciliation", force: true, reconciled: &now, wantRan: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &stubStorageReconciliationRunner{report: &service.StorageReconciliationReport{TrackedBlobs: 3}}
			quota := &model.SystemStorageQuota{ReconciledAt: tt.reconciled}
			if tt.reconciled != nil {
				quota.StorageID = &storageID
			}
			report, ran, err := reconcileStorageIfNeeded(
				context.Background(),
				tt.force,
				&stubReconciliationStateReader{quota: quota},
				runner,
			)
			if err != nil {
				t.Fatalf("reconcileStorageIfNeeded() error = %v", err)
			}
			wantCalls := 0
			if tt.wantRan {
				wantCalls = 1
			}
			if ran != tt.wantRan || runner.calls != wantCalls {
				t.Fatalf("ran=%t calls=%d, want ran=%t", ran, runner.calls, tt.wantRan)
			}
			if tt.wantRan && report.TrackedBlobs != 3 {
				t.Fatalf("reconciliation report = %+v", report)
			}
		})
	}
}

func TestNewRuntimeStorageDependencies(t *testing.T) {
	t.Run("local creates its root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "local")
		dependencies, err := newRuntimeStorageDependencies(config.Config{
			StorageMode: config.StorageModeLocal,
			StorageRoot: root,
		})
		if err != nil {
			t.Fatalf("new local storage dependencies: %v", err)
		}
		if dependencies.lifecycle == nil || dependencies.reader == nil || dependencies.cleanup == nil ||
			dependencies.reconciliation == nil || dependencies.readiness == nil || dependencies.identity == nil {
			t.Fatal("local storage dependencies are incomplete")
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			t.Fatalf("local storage root stat: info=%v err=%v", info, err)
		}
	})

	t.Run("shared filesystem requires a provisioned mount", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "missing")
		_, err := newRuntimeStorageDependencies(config.Config{
			StorageMode: config.StorageModeSharedFilesystem,
			StorageRoot: root,
		})
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("new shared storage dependencies error = %v, want not exist", err)
		}
		if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("shared storage root was created unexpectedly: %v", statErr)
		}
	})

	t.Run("shared filesystem wires every caller view", func(t *testing.T) {
		root := t.TempDir()
		dependencies, err := newRuntimeStorageDependencies(config.Config{
			StorageMode: config.StorageModeSharedFilesystem,
			StorageRoot: root,
		})
		if err != nil {
			t.Fatalf("new shared storage dependencies: %v", err)
		}
		if dependencies.lifecycle == nil || dependencies.reader == nil || dependencies.cleanup == nil ||
			dependencies.reconciliation == nil || dependencies.readiness == nil {
			t.Fatal("shared storage dependencies are incomplete")
		}
		if err := dependencies.readiness.CheckReady(context.Background()); err != nil {
			t.Fatalf("shared storage readiness: %v", err)
		}
	})
}
