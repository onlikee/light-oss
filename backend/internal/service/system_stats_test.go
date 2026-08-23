package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMapRuntimeOS(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		goos string
		want string
	}{
		{goos: "windows", want: "windows"},
		{goos: "linux", want: "linux"},
		{goos: "darwin", want: "macos"},
		{goos: "freebsd", want: "other"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.goos, func(t *testing.T) {
			t.Parallel()

			if got := mapRuntimeOS(tc.goos); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestDedupePartitionsByMountPoint(t *testing.T) {
	t.Parallel()

	partitions := []partitionLike{
		{Mountpoint: currentPath("/"), Device: "disk-a"},
		{Mountpoint: currentPath("/"), Device: "disk-b"},
		{Mountpoint: currentPath("/data"), Device: "disk-c"},
	}

	items := dedupePartitionLikes(partitions)
	if len(items) != 2 {
		t.Fatalf("expected 2 partitions after dedupe, got %d", len(items))
	}
	if items[0].Device != "disk-a" {
		t.Fatalf("expected first partition to be preserved, got %q", items[0].Device)
	}
}

func TestMarkStorageRootDiskPrefersLongestMountPoint(t *testing.T) {
	t.Parallel()

	storageRoot := currentPath("/var/lib/light-oss/storage")
	disks := []diskSnapshot{
		{label: "root", mountPoint: currentPath("/")},
		{label: "var", mountPoint: currentPath("/var")},
		{label: "light-oss", mountPoint: currentPath("/var/lib/light-oss")},
	}

	markStorageRootDisk(storageRoot, disks)

	if disks[0].containsStorageRoot {
		t.Fatalf("expected root mount point to lose longest-match selection")
	}
	if disks[1].containsStorageRoot {
		t.Fatalf("expected /var mount point to lose longest-match selection")
	}
	if !disks[2].containsStorageRoot {
		t.Fatalf("expected longest matching mount point to be marked")
	}
}

func TestSortDiskSnapshotsPrioritizesStorageRoot(t *testing.T) {
	t.Parallel()

	disks := []diskSnapshot{
		{label: "zeta", mountPoint: currentPath("/zeta")},
		{label: "alpha", mountPoint: currentPath("/alpha")},
		{label: "beta", mountPoint: currentPath("/beta"), containsStorageRoot: true},
	}

	sortDiskSnapshots(disks)

	if disks[0].label != "beta" {
		t.Fatalf("expected storage root disk first, got %q", disks[0].label)
	}
	if disks[1].label != "alpha" || disks[2].label != "zeta" {
		t.Fatalf("expected remaining disks to be sorted by label, got %+v", disks)
	}
}

func TestDirectorySize(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("1234"), 0o644); err != nil {
		t.Fatalf("write alpha file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "beta.txt"), []byte("123456"), 0o644); err != nil {
		t.Fatalf("write beta file: %v", err)
	}

	size, err := directorySize(root)
	if err != nil {
		t.Fatalf("directory size: %v", err)
	}
	if size != 10 {
		t.Fatalf("expected usage to be 10, got %d", size)
	}
}

func TestDirectorySizeProbeCanCountCalls(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "object.bin"), []byte("1234"), 0o644); err != nil {
		t.Fatalf("write object file: %v", err)
	}

	originalDirectorySize := directorySize
	t.Cleanup(func() {
		directorySize = originalDirectorySize
	})

	callCount := 0
	directorySize = func(root string) (uint64, error) {
		callCount++
		return calculateDirectorySize(root)
	}

	size, err := directorySize(root)
	if err != nil {
		t.Fatalf("directory size: %v", err)
	}
	if size != 4 {
		t.Fatalf("expected usage to be 4, got %d", size)
	}
	if callCount != 1 {
		t.Fatalf("expected directory size probe to count 1 call, got %d", callCount)
	}
}

type partitionLike struct {
	Device     string
	Mountpoint string
}

func dedupePartitionLikes(partitions []partitionLike) []partitionLike {
	seen := make(map[string]struct{}, len(partitions))
	result := make([]partitionLike, 0, len(partitions))

	for _, partition := range partitions {
		key := normalizedPathForComparison(partition.Mountpoint)
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		result = append(result, partition)
	}

	return result
}

func currentPath(path string) string {
	if runtime.GOOS == "windows" {
		return filepath.Clean(`C:` + filepath.FromSlash(path))
	}

	return filepath.Clean(path)
}
