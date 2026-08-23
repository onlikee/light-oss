package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/storage"
)

type fakeBlobStore struct {
	mu             sync.Mutex
	identity       string
	files          map[string][]byte
	stageErr       error
	commitErr      error
	deleteErr      error
	deleteFailures int
	stageCalls     []string
	commitCalls    [][2]string
	deleteCalls    []string
}

func newFakeBlobStore() *fakeBlobStore {
	return &fakeBlobStore{
		identity: "11111111-1111-4111-8111-111111111111",
		files:    make(map[string][]byte),
	}
}

func (s *fakeBlobStore) Stage(
	ctx context.Context,
	path string,
	reader io.Reader,
	beforeWrite func(int64) error,
) (*storage.StoredFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(data) > 0 && beforeWrite != nil {
		if err := beforeWrite(int64(len(data))); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.stageCalls = append(s.stageCalls, path)
	s.files[path] = bytes.Clone(data)
	if s.stageErr != nil {
		return nil, s.stageErr
	}

	return &storage.StoredFile{
		RelativePath: path,
		Size:         int64(len(data)),
		ETag:         fmt.Sprintf("etag-%d", len(data)),
	}, nil
}

func (s *fakeBlobStore) Commit(stagingPath string, finalPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitCalls = append(s.commitCalls, [2]string{stagingPath, finalPath})
	if s.commitErr != nil {
		return s.commitErr
	}

	data, ok := s.files[stagingPath]
	if !ok {
		return &os.PathError{Op: "rename", Path: stagingPath, Err: os.ErrNotExist}
	}
	s.files[finalPath] = bytes.Clone(data)
	delete(s.files, stagingPath)
	return nil
}

func (s *fakeBlobStore) Open(path string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[path]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(data))), nil
}

func (s *fakeBlobStore) Delete(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls = append(s.deleteCalls, path)
	if s.deleteFailures > 0 {
		s.deleteFailures--
		return s.deleteErr
	}
	delete(s.files, path)
	return nil
}

func (s *fakeBlobStore) Stat(path string) (os.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[path]
	if !ok {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
	}
	return fakeBlobFileInfo{name: path, size: int64(len(data))}, nil
}

func (s *fakeBlobStore) Identity(context.Context) (string, error) {
	return s.identity, nil
}

func (s *fakeBlobStore) WalkManaged(ctx context.Context) ([]storage.ManagedFileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paths := make([]string, 0, len(s.files))
	for path := range s.files {
		if strings.HasPrefix(path, "objects/") || strings.HasPrefix(path, "staging/") {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	files := make([]storage.ManagedFileInfo, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		files = append(files, storage.ManagedFileInfo{
			RelativePath: path,
			Size:         int64(len(s.files[path])),
			ModifiedAt:   time.Unix(0, 0).UTC(),
		})
	}
	return files, nil
}

func (s *fakeBlobStore) put(path string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = bytes.Clone(data)
}

func (s *fakeBlobStore) has(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.files[path]
	return ok
}

func (s *fakeBlobStore) fileCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.files)
}

type fakeBlobFileInfo struct {
	name string
	size int64
}

func (i fakeBlobFileInfo) Name() string       { return i.name }
func (i fakeBlobFileInfo) Size() int64        { return i.size }
func (i fakeBlobFileInfo) Mode() os.FileMode  { return 0 }
func (i fakeBlobFileInfo) ModTime() time.Time { return time.Unix(0, 0).UTC() }
func (i fakeBlobFileInfo) IsDir() bool        { return false }
func (i fakeBlobFileInfo) Sys() any           { return nil }

func newStorageLifecycleTestDB(t *testing.T, maxBytes uint64) (*gorm.DB, *repository.StorageBlobRepository) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=foreign_keys(1)", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	if err := db.AutoMigrate(
		&model.SystemStorageQuota{},
		&model.StorageBlob{},
		&model.StorageCleanupJob{},
		&model.Bucket{},
		&model.Object{},
		&model.RecycleBinObject{},
	); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&model.SystemStorageQuota{
		ID:        1,
		MaxBytes:  maxBytes,
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create storage quota: %v", err)
	}

	return db, repository.NewStorageBlobRepository(db)
}

func loadStorageLifecycleQuota(t *testing.T, db *gorm.DB) model.SystemStorageQuota {
	t.Helper()
	var quota model.SystemStorageQuota
	if err := db.First(&quota, "id = ?", 1).Error; err != nil {
		t.Fatalf("load storage quota: %v", err)
	}
	return quota
}

func countStorageLifecycleRows(t *testing.T, db *gorm.DB, value any) int64 {
	t.Helper()
	var count int64
	if err := db.Model(value).Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}
