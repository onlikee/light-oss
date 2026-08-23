package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// SharedFilesystemStorage reuses LocalStorage's file operations on a shared
// filesystem. The mounted root must provide read-write-many access, coherent
// visibility between instances, and atomic rename within the same filesystem.
type SharedFilesystemStorage struct {
	*LocalStorage
}

func NewSharedFilesystemStorage(root string) (*SharedFilesystemStorage, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat shared storage root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("shared storage root is not a directory")
	}

	return &SharedFilesystemStorage{LocalStorage: NewLocalStorage(root)}, nil
}

func (s *SharedFilesystemStorage) CheckReady(ctx context.Context) error {
	probeID := uuid.NewString()
	stagingPath := filepath.ToSlash(filepath.Join("staging", readinessProbePrefix+probeID+".tmp"))
	finalPath := filepath.ToSlash(filepath.Join("objects", readinessProbePrefix+probeID+".bin"))
	defer func() {
		_ = s.Delete(stagingPath)
		_ = s.Delete(finalPath)
	}()

	probeContents := []byte("light-oss-shared-filesystem-readiness")
	if _, err := s.Stage(ctx, stagingPath, bytes.NewReader(probeContents), nil); err != nil {
		return fmt.Errorf("stage shared storage readiness probe: %w", err)
	}
	if err := s.Commit(stagingPath, finalPath); err != nil {
		return fmt.Errorf("commit shared storage readiness probe: %w", err)
	}
	reader, err := s.Open(finalPath)
	if err != nil {
		return fmt.Errorf("open shared storage readiness probe: %w", err)
	}
	contents, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("read shared storage readiness probe: %w", errors.Join(readErr, closeErr))
	}
	if !bytes.Equal(contents, probeContents) {
		return fmt.Errorf("shared storage readiness probe contents changed after rename")
	}
	if err := s.Delete(finalPath); err != nil {
		return fmt.Errorf("delete shared storage readiness probe: %w", err)
	}
	if err := s.Delete(finalPath); err != nil {
		return fmt.Errorf("repeat shared storage readiness probe delete: %w", err)
	}
	return ctx.Err()
}
