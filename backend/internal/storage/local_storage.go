package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type StoredFile struct {
	RelativePath string
	Size         int64
	ETag         string
}

type LocalStorage struct {
	root string
}

type ManagedFileInfo struct {
	RelativePath string
	Size         int64
	ModifiedAt   time.Time
}

const readinessProbePrefix = ".readiness-"
const storageIdentityFilename = ".storage-id"

func NewLocalStorage(root string) *LocalStorage {
	return &LocalStorage{root: root}
}

func (s *LocalStorage) Stage(
	ctx context.Context,
	stagingPath string,
	reader io.Reader,
	beforeWrite func(int64) error,
) (*StoredFile, error) {
	if hasTraversal(stagingPath) {
		return nil, fmt.Errorf("invalid storage path")
	}

	absPath := filepath.Join(s.root, filepath.FromSlash(stagingPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(absPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}

	hasher := sha256.New()
	buffer := make([]byte, 128*1024)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			_ = os.Remove(absPath)
			return nil, err
		}

		n, readErr := reader.Read(buffer)
		if n > 0 {
			if beforeWrite != nil {
				if err := beforeWrite(size + int64(n)); err != nil {
					_ = file.Close()
					_ = os.Remove(absPath)
					return nil, err
				}
			}

			written, writeErr := file.Write(buffer[:n])
			if written > 0 {
				_, _ = hasher.Write(buffer[:written])
				size += int64(written)
			}
			if writeErr != nil {
				_ = file.Close()
				_ = os.Remove(absPath)
				return nil, writeErr
			}
			if written != n {
				_ = file.Close()
				_ = os.Remove(absPath)
				return nil, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			_ = file.Close()
			_ = os.Remove(absPath)
			return nil, readErr
		}
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(absPath)
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(absPath)
		return nil, err
	}

	return &StoredFile{
		RelativePath: stagingPath,
		Size:         size,
		ETag:         hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func (s *LocalStorage) Commit(stagingPath string, finalPath string) error {
	if hasTraversal(stagingPath) || hasTraversal(finalPath) {
		return fmt.Errorf("invalid storage path")
	}

	stagingAbsPath := filepath.Join(s.root, filepath.FromSlash(stagingPath))
	finalAbsPath := filepath.Join(s.root, filepath.FromSlash(finalPath))
	if _, err := os.Stat(finalAbsPath); err == nil {
		if removeErr := os.Remove(stagingAbsPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return removeErr
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(finalAbsPath), 0o755); err != nil {
		return err
	}

	return os.Rename(stagingAbsPath, finalAbsPath)
}

func (s *LocalStorage) Open(relativePath string) (io.ReadCloser, error) {
	if hasTraversal(relativePath) {
		return nil, fmt.Errorf("invalid storage path")
	}

	return os.Open(filepath.Join(s.root, filepath.FromSlash(relativePath)))
}

func (s *LocalStorage) Delete(relativePath string) error {
	if hasTraversal(relativePath) {
		return fmt.Errorf("invalid storage path")
	}

	err := os.Remove(filepath.Join(s.root, filepath.FromSlash(relativePath)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

func (s *LocalStorage) Stat(relativePath string) (os.FileInfo, error) {
	if hasTraversal(relativePath) {
		return nil, fmt.Errorf("invalid storage path")
	}

	return os.Stat(filepath.Join(s.root, filepath.FromSlash(relativePath)))
}

func (s *LocalStorage) CheckReady(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	info, err := os.Stat(s.root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("storage root is not a directory")
	}

	probe, err := os.CreateTemp(s.root, ".readiness-*")
	if err != nil {
		return err
	}
	probePath := probe.Name()
	committedPath := probePath + ".committed"
	defer func() {
		_ = os.Remove(probePath)
		_ = os.Remove(committedPath)
	}()
	probeContents := []byte("light-oss-readiness")
	if _, err := probe.Write(probeContents); err != nil {
		_ = probe.Close()
		return err
	}
	if err := probe.Sync(); err != nil {
		_ = probe.Close()
		return err
	}
	if err := probe.Close(); err != nil {
		return err
	}
	if err := os.Rename(probePath, committedPath); err != nil {
		return err
	}
	contents, err := os.ReadFile(committedPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(contents, probeContents) {
		return fmt.Errorf("storage readiness probe contents changed after rename")
	}
	if err := os.Remove(committedPath); err != nil {
		return err
	}

	return ctx.Err()
}

func (s *LocalStorage) Identity(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	identityPath := filepath.Join(s.root, storageIdentityFilename)
	readIdentity := func() (string, error) {
		contents, err := os.ReadFile(identityPath)
		if err != nil {
			return "", err
		}
		identity := strings.TrimSpace(string(contents))
		if _, err := uuid.Parse(identity); err != nil {
			return "", fmt.Errorf("storage identity is invalid: %w", err)
		}
		return identity, nil
	}

	identity, err := readIdentity()
	if err == nil {
		return identity, ctx.Err()
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	identity = uuid.NewString()
	file, err := os.OpenFile(identityPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return readIdentity()
	}
	if err != nil {
		return "", err
	}
	removeIdentity := true
	defer func() {
		_ = file.Close()
		if removeIdentity {
			_ = os.Remove(identityPath)
		}
	}()
	if _, err := io.WriteString(file, identity+"\n"); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	removeIdentity = false
	return identity, ctx.Err()
}

func (s *LocalStorage) WalkManaged(ctx context.Context) ([]ManagedFileInfo, error) {
	files := make([]ManagedFileInfo, 0)
	for _, namespace := range []string{"objects", "staging", "tmp"} {
		namespaceRoot := filepath.Join(s.root, namespace)
		err := filepath.WalkDir(namespaceRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) && path == namespaceRoot {
					return filepath.SkipDir
				}
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if strings.HasPrefix(entry.Name(), readinessProbePrefix) {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return err
			}
			relativePath, err := filepath.Rel(s.root, path)
			if err != nil {
				return err
			}
			files = append(files, ManagedFileInfo{
				RelativePath: filepath.ToSlash(relativePath),
				Size:         info.Size(),
				ModifiedAt:   info.ModTime(),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return files, nil
}

func hasTraversal(relativePath string) bool {
	cleaned := filepath.Clean(relativePath)
	return filepath.IsAbs(cleaned) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

func NewManagedPath(namespace string, extension string) string {
	fileID := strings.ReplaceAll(uuid.NewString(), "-", "")
	return filepath.ToSlash(filepath.Join(namespace, fileID[0:2], fileID[2:4], fileID+extension))
}
