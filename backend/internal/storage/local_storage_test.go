package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestLocalStorageRejectsPathsOutsideRoot(t *testing.T) {
	invalidPaths := []string{
		"../outside.bin",
		filepath.Join(t.TempDir(), "absolute.bin"),
	}
	if runtime.GOOS == "windows" {
		invalidPaths = append(invalidPaths, `..\outside.bin`)
	}

	for _, invalidPath := range invalidPaths {
		invalidPath := invalidPath
		t.Run(strings.ReplaceAll(invalidPath, string(filepath.Separator), "_"), func(t *testing.T) {
			root := t.TempDir()
			store := NewLocalStorage(root)

			operations := []struct {
				name string
				run  func() error
			}{
				{
					name: "stage",
					run: func() error {
						_, err := store.Stage(context.Background(), invalidPath, strings.NewReader("data"), nil)
						return err
					},
				},
				{name: "commit source", run: func() error { return store.Commit(invalidPath, "objects/final.bin") }},
				{name: "commit destination", run: func() error { return store.Commit("staging/source.tmp", invalidPath) }},
				{
					name: "open",
					run: func() error {
						reader, err := store.Open(invalidPath)
						if reader != nil {
							_ = reader.Close()
						}
						return err
					},
				},
				{name: "delete", run: func() error { return store.Delete(invalidPath) }},
				{
					name: "stat",
					run: func() error {
						_, err := store.Stat(invalidPath)
						return err
					},
				},
			}

			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					err := operation.run()
					if err == nil || !strings.Contains(err.Error(), "invalid storage path") {
						t.Fatalf("operation error = %v, want invalid storage path", err)
					}
				})
			}
		})
	}
}

func TestLocalStorageStageRemovesPartialFileWhenReservationFails(t *testing.T) {
	root := t.TempDir()
	store := NewLocalStorage(root)
	stagingPath := "staging/partial.tmp"
	reserveErr := errors.New("quota reservation failed")

	_, err := store.Stage(
		context.Background(),
		stagingPath,
		bytes.NewReader(bytes.Repeat([]byte("x"), 256*1024)),
		func(int64) error { return reserveErr },
	)
	if !errors.Is(err, reserveErr) {
		t.Fatalf("stage error = %v, want %v", err, reserveErr)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(stagingPath))); !os.IsNotExist(err) {
		t.Fatalf("partial staging file stat error = %v, want not exist", err)
	}
}

func TestLocalStorageWalkManagedIncludesLegacyTemporaryFilesAndExcludesUnmanagedFiles(t *testing.T) {
	root := t.TempDir()
	writeLocalStorageTestFile(t, root, "objects/a.bin", "object")
	writeLocalStorageTestFile(t, root, "staging/b.tmp", "stage")
	writeLocalStorageTestFile(t, root, "objects/.readiness-object.bin", "probe")
	writeLocalStorageTestFile(t, root, "staging/.readiness-staging.tmp", "probe")
	writeLocalStorageTestFile(t, root, "tmp/c.tmp", "temporary")
	writeLocalStorageTestFile(t, root, "unmanaged.bin", "unmanaged")
	store := NewLocalStorage(root)

	files, err := store.WalkManaged(context.Background())
	if err != nil {
		t.Fatalf("walk managed storage: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("managed files = %+v, want 3", files)
	}
	paths := map[string]bool{}
	for _, file := range files {
		paths[file.RelativePath] = true
	}
	if !paths["objects/a.bin"] || !paths["staging/b.tmp"] || !paths["tmp/c.tmp"] {
		t.Fatalf("managed paths = %+v", paths)
	}
}

func TestLocalStorageCheckReadyLeavesNoProbeFile(t *testing.T) {
	root := t.TempDir()
	store := NewLocalStorage(root)

	if err := store.CheckReady(context.Background()); err != nil {
		t.Fatalf("check ready: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read storage root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected readiness probe cleanup, found %+v", entries)
	}
}

func TestLocalStorageIdentityIsStableAndRootSpecific(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	first := NewLocalStorage(root)
	second := NewLocalStorage(root)
	firstID, err := first.Identity(ctx)
	if err != nil {
		t.Fatalf("create storage identity: %v", err)
	}
	secondID, err := second.Identity(ctx)
	if err != nil {
		t.Fatalf("read storage identity from second instance: %v", err)
	}
	if firstID != secondID {
		t.Fatalf("shared root identities differ: first=%q second=%q", firstID, secondID)
	}

	otherID, err := NewLocalStorage(t.TempDir()).Identity(ctx)
	if err != nil {
		t.Fatalf("create second root identity: %v", err)
	}
	if otherID == firstID {
		t.Fatalf("different roots unexpectedly share identity %q", firstID)
	}
	files, err := first.WalkManaged(ctx)
	if err != nil {
		t.Fatalf("walk storage with identity sentinel: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("storage identity sentinel appeared as managed content: %+v", files)
	}
}

func TestLocalStorageCommitAndDeleteAreIdempotentAcrossInstances(t *testing.T) {
	root := t.TempDir()
	first := NewLocalStorage(root)
	second := NewLocalStorage(root)
	stagingPath := "staging/shared.tmp"
	finalPath := "objects/shared.bin"

	if _, err := first.Stage(context.Background(), stagingPath, strings.NewReader("shared-data"), nil); err != nil {
		t.Fatalf("stage shared content: %v", err)
	}
	if err := first.Commit(stagingPath, finalPath); err != nil {
		t.Fatalf("commit shared content: %v", err)
	}
	if err := second.Commit(stagingPath, finalPath); err != nil {
		t.Fatalf("repeat commit from second instance: %v", err)
	}

	reader, err := second.Open(finalPath)
	if err != nil {
		t.Fatalf("open committed content from second instance: %v", err)
	}
	contents, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read committed content: read=%v close=%v", err, closeErr)
	}
	if string(contents) != "shared-data" {
		t.Fatalf("committed content = %q, want shared-data", contents)
	}

	if err := second.Delete(finalPath); err != nil {
		t.Fatalf("delete shared content: %v", err)
	}
	if err := first.Delete(finalPath); err != nil {
		t.Fatalf("repeat delete from first instance: %v", err)
	}
}

func TestSharedFilesystemStorageRequiresExistingRoot(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "missing")
	if _, err := NewSharedFilesystemStorage(missingRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("NewSharedFilesystemStorage() error = %v, want not exist", err)
	}

	root := t.TempDir()
	store, err := NewSharedFilesystemStorage(root)
	if err != nil {
		t.Fatalf("NewSharedFilesystemStorage() error = %v", err)
	}
	if err := store.CheckReady(context.Background()); err != nil {
		t.Fatalf("shared filesystem readiness: %v", err)
	}
	files, err := store.WalkManaged(context.Background())
	if err != nil {
		t.Fatalf("walk shared filesystem after readiness: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("shared filesystem readiness left managed files: %+v", files)
	}
}

func TestSharedFilesystemReadinessDoesNotRaceWithManagedWalk(t *testing.T) {
	root := t.TempDir()
	store, err := NewSharedFilesystemStorage(root)
	if err != nil {
		t.Fatalf("NewSharedFilesystemStorage() error = %v", err)
	}

	const probes = 32
	start := make(chan struct{})
	errors := make(chan error, probes)
	var waitGroup sync.WaitGroup
	for range probes {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			if err := store.CheckReady(context.Background()); err != nil {
				errors <- err
			}
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(done)
	}()
	for {
		files, err := store.WalkManaged(context.Background())
		if err != nil {
			t.Fatalf("walk managed storage while probing readiness: %v", err)
		}
		if len(files) != 0 {
			t.Fatalf("readiness probes appeared as managed files: %+v", files)
		}
		select {
		case <-done:
			close(errors)
			for err := range errors {
				t.Errorf("shared filesystem readiness: %v", err)
			}
			return
		default:
		}
	}
}

func writeLocalStorageTestFile(t *testing.T, root string, relativePath string, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}
