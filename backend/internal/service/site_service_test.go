package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	"light-oss/backend/internal/repository"
	"light-oss/backend/internal/storage"
)

func TestSiteServiceFindByDomainNormalizesHost(t *testing.T) {
	bucketRepo, objectService, siteService := newTestSiteServices(t)
	ctx := context.Background()

	if err := bucketRepo.Create(ctx, &model.Bucket{Name: "websites"}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	_, err := objectService.Upload(ctx, UploadObjectInput{
		BucketName:       "websites",
		ObjectKey:        "demo/index.html",
		Visibility:       "public",
		OriginalFilename: "index.html",
		ContentType:      "text/html",
		Body:             stringsReader("<html>home</html>"),
	})
	if err != nil {
		t.Fatalf("upload index: %v", err)
	}

	created, err := siteService.Create(ctx, SiteInput{
		BucketName: "websites",
		RootPrefix: "demo/",
		Enabled:    true,
		Domains:    []string{"demo.localhost"},
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}

	site, err := siteService.FindByDomain(ctx, "Demo.Localhost:80")
	if err != nil {
		t.Fatalf("find by domain: %v", err)
	}
	if site == nil || site.ID != created.ID {
		t.Fatalf("expected normalized host to resolve site, got %+v", site)
	}
}

func TestSiteServiceOpenContentUsesFallbacksAndHidesPrivateObjects(t *testing.T) {
	bucketRepo, objectService, siteService := newTestSiteServices(t)
	ctx := context.Background()

	if err := bucketRepo.Create(ctx, &model.Bucket{Name: "websites"}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	uploads := []UploadObjectInput{
		{
			BucketName:       "websites",
			ObjectKey:        "demo/index.html",
			Visibility:       "public",
			OriginalFilename: "index.html",
			ContentType:      "text/html",
			Body:             stringsReader("<html>home</html>"),
		},
		{
			BucketName:       "websites",
			ObjectKey:        "demo/404.html",
			Visibility:       "public",
			OriginalFilename: "404.html",
			ContentType:      "text/html",
			Body:             stringsReader("<html>missing</html>"),
		},
		{
			BucketName:       "websites",
			ObjectKey:        "demo/secret.txt",
			Visibility:       "private",
			OriginalFilename: "secret.txt",
			ContentType:      "text/plain",
			Body:             stringsReader("hidden"),
		},
	}
	for _, input := range uploads {
		if _, err := objectService.Upload(ctx, input); err != nil {
			t.Fatalf("upload %s: %v", input.ObjectKey, err)
		}
	}

	site, err := siteService.Create(ctx, SiteInput{
		BucketName:  "websites",
		RootPrefix:  "demo/",
		Enabled:     true,
		SPAFallback: true,
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}

	spaContent, err := siteService.OpenContent(ctx, site, "/dashboard/settings")
	if err != nil {
		t.Fatalf("open spa fallback: %v", err)
	}
	defer func() { _ = spaContent.Reader.Close() }()
	if spaContent.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", spaContent.StatusCode)
	}
	if body := readAll(t, spaContent.Reader); body != "<html>home</html>" {
		t.Fatalf("unexpected spa fallback body %q", body)
	}

	updated, err := siteService.Update(ctx, site.ID, SiteInput{
		BucketName:    "websites",
		RootPrefix:    "demo/",
		Enabled:       true,
		ErrorDocument: "404.html",
		Domains:       []string{"demo.localhost"},
	})
	if err != nil {
		t.Fatalf("update site: %v", err)
	}

	errorContent, err := siteService.OpenContent(ctx, updated, "/missing/path")
	if err != nil {
		t.Fatalf("open error document: %v", err)
	}
	defer func() { _ = errorContent.Reader.Close() }()
	if errorContent.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", errorContent.StatusCode)
	}
	if body := readAll(t, errorContent.Reader); body != "<html>missing</html>" {
		t.Fatalf("unexpected error document body %q", body)
	}

	privateContent, err := siteService.OpenContent(ctx, updated, "/secret.txt")
	if err != nil {
		t.Fatalf("open private content path: %v", err)
	}
	defer func() { _ = privateContent.Reader.Close() }()
	if privateContent.StatusCode != 404 {
		t.Fatalf("expected 404 for masked private path, got %d", privateContent.StatusCode)
	}
	if body := readAll(t, privateContent.Reader); body != "<html>missing</html>" {
		t.Fatalf("unexpected private fallback body %q", body)
	}
}

func TestNormalizeSiteDomainAcceptsCustomHostnames(t *testing.T) {
	validDomains := map[string]string{
		"Demo.Example.Com":      "demo.example.com",
		"example.com":           "example.com",
		"www.demo.example.com.": "www.demo.example.com",
		"demo.localhost":        "demo.localhost",
	}
	for input, expected := range validDomains {
		valid, err := NormalizeSiteDomain(input)
		if err != nil {
			t.Fatalf("expected %q to be valid, got %v", input, err)
		}
		if valid != expected {
			t.Fatalf("expected normalized domain %q, got %q", expected, valid)
		}
	}
}

func TestNormalizeSiteDomainRejectsInvalidHostnames(t *testing.T) {
	invalidDomains := []string{
		"",
		"http://example.com",
		"example.com/path",
		"example.com?debug=true",
		"example.com:8080",
		"-example.com",
		"example-.com",
		"example..com",
		"exa_mple.com",
		strings.Repeat("a", 64) + ".example.com",
	}
	for _, domain := range invalidDomains {
		if _, err := NormalizeSiteDomain(domain); err == nil {
			t.Fatalf("expected %q to be rejected", domain)
		}
	}
}

func newTestSiteServices(t *testing.T) (*repository.BucketRepository, *ObjectService, *SiteService) {
	t.Helper()

	dsn := fmt.Sprintf("file:%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatalf("enable sqlite foreign keys: %v", err)
	}
	if err := db.AutoMigrate(&model.Bucket{}, &model.SystemStorageQuota{}, &model.Object{}, &model.RecycleBinObject{}, &model.Site{}, &model.SiteDomain{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	root := t.TempDir()
	bucketRepo := repository.NewBucketRepository(db)
	objectRepo := repository.NewObjectRepository(db)
	recycleRepo := repository.NewRecycleBinRepository(db)
	siteRepo := repository.NewSiteRepository(db)
	localStorage := storage.NewLocalStorage(root)
	storageQuotaRepo := repository.NewStorageQuotaRepository(db)
	storageQuotaService := NewStorageQuotaService(zap.NewNop(), root, localStorage, objectRepo, recycleRepo, storageQuotaRepo)
	objectService := NewObjectService(db, bucketRepo, objectRepo, recycleRepo, localStorage, storageQuotaService)
	siteService := NewSiteService(bucketRepo, siteRepo, objectService)
	return bucketRepo, objectService, siteService
}

func stringsReader(value string) io.Reader {
	return strings.NewReader(value)
}

func readAll(t *testing.T, reader io.Reader) string {
	t.Helper()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	return string(body)
}
