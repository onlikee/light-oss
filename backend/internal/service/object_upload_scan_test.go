package service

import (
	"context"
	"io"
	"strings"
	"testing"

	"light-oss/backend/internal/model"
)

func TestObjectUploadsDoNotScanStorageDirectory(t *testing.T) {
	bucketRepo, objectService, _ := newTestSiteServices(t)
	ctx := context.Background()
	if err := bucketRepo.Create(ctx, &model.Bucket{Name: "uploads"}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	originalDirectorySize := directorySize
	t.Cleanup(func() {
		directorySize = originalDirectorySize
	})

	scanCount := 0
	directorySize = func(root string) (uint64, error) {
		scanCount++
		return calculateDirectorySize(root)
	}

	upload := func(allowOverwrite bool) {
		t.Helper()
		if _, err := objectService.Upload(ctx, UploadObjectInput{
			BucketName:       "uploads",
			ObjectKey:        "single.txt",
			Visibility:       "private",
			AllowOverwrite:   allowOverwrite,
			OriginalFilename: "single.txt",
			ContentType:      "text/plain",
			Body:             strings.NewReader("payload"),
		}); err != nil {
			t.Fatalf("upload object with overwrite=%t: %v", allowOverwrite, err)
		}
	}

	upload(false)
	upload(true)
	if _, err := objectService.UploadBatch(ctx, UploadObjectBatchInput{
		BucketName: "uploads",
		Prefix:     "batch/",
		Visibility: "private",
		Items: []UploadObjectBatchItemInput{
			{
				RelativePath:     "first.txt",
				OriginalFilename: "first.txt",
				ContentType:      "text/plain",
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("first")), nil
				},
			},
			{
				RelativePath:     "second.txt",
				OriginalFilename: "second.txt",
				ContentType:      "text/plain",
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("second")), nil
				},
			},
		},
	}); err != nil {
		t.Fatalf("upload object batch: %v", err)
	}

	if scanCount != 0 {
		t.Fatalf("expected upload paths not to scan the storage directory, got %d calls", scanCount)
	}
}
