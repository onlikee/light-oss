package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReadsRootEnvWhenPersonalMissing(t *testing.T) {
	resetConfigEnv(t)
	prepareBackendWorkspace(t, map[string]string{
		".env": strings.Join([]string{
			"DB_DSN=root-env-dsn",
			"APP_STORAGE_ROOT=./storage-from-env",
			"APP_BEARER_TOKENS=root-token",
			"APP_SIGNING_SECRET=root-secret",
			"",
		}, "\n"),
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DatabaseDSN != "root-env-dsn" {
		t.Fatalf("expected DB_DSN from .env, got %q", cfg.DatabaseDSN)
	}
	if cfg.StorageRoot != "./storage-from-env" {
		t.Fatalf("expected APP_STORAGE_ROOT from .env, got %q", cfg.StorageRoot)
	}
}

func TestLoadReadsPersonalEnvBeforeRootEnv(t *testing.T) {
	resetConfigEnv(t)
	prepareBackendWorkspace(t, map[string]string{
		".env": strings.Join([]string{
			"DB_DSN=root-env-dsn",
			"APP_STORAGE_ROOT=./storage-from-env",
			"APP_BEARER_TOKENS=root-token",
			"APP_SIGNING_SECRET=root-secret",
			"",
		}, "\n"),
		".env.personal": strings.Join([]string{
			"DB_DSN=personal-env-dsn",
			"APP_STORAGE_ROOT=./storage-from-personal",
			"APP_BEARER_TOKENS=personal-token",
			"APP_SIGNING_SECRET=personal-secret",
			"",
		}, "\n"),
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DatabaseDSN != "personal-env-dsn" {
		t.Fatalf("expected DB_DSN from .env.personal, got %q", cfg.DatabaseDSN)
	}
	if cfg.StorageRoot != "./storage-from-personal" {
		t.Fatalf("expected APP_STORAGE_ROOT from .env.personal, got %q", cfg.StorageRoot)
	}
	if got := strings.Join(cfg.BearerTokens, ","); got != "personal-token" {
		t.Fatalf("expected APP_BEARER_TOKENS from .env.personal, got %q", got)
	}
}

func TestLoadDoesNotFallbackToRootEnvWhenPersonalExists(t *testing.T) {
	resetConfigEnv(t)
	prepareBackendWorkspace(t, map[string]string{
		".env": strings.Join([]string{
			"DB_DSN=root-env-dsn",
			"APP_STORAGE_ROOT=./storage-from-env",
			"APP_BEARER_TOKENS=root-token",
			"APP_SIGNING_SECRET=root-secret",
			"",
		}, "\n"),
		".env.personal": strings.Join([]string{
			"DB_DSN=",
			"APP_STORAGE_ROOT=./storage-from-personal",
			"APP_BEARER_TOKENS=personal-token",
			"APP_SIGNING_SECRET=personal-secret",
			"",
		}, "\n"),
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load() to fail when .env.personal omits a required value")
	}
	if !strings.Contains(err.Error(), "DB_DSN is required") {
		t.Fatalf("expected DB_DSN validation error, got %v", err)
	}
}

func TestLoadAllowsShellEnvToOverridePersonalEnv(t *testing.T) {
	resetConfigEnv(t)
	prepareBackendWorkspace(t, map[string]string{
		".env.personal": strings.Join([]string{
			"DB_DSN=personal-env-dsn",
			"APP_STORAGE_ROOT=./storage-from-personal",
			"APP_BEARER_TOKENS=personal-token",
			"APP_SIGNING_SECRET=personal-secret",
			"",
		}, "\n"),
	})
	t.Setenv("DB_DSN", "shell-env-dsn")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DatabaseDSN != "shell-env-dsn" {
		t.Fatalf("expected shell env to override file config, got %q", cfg.DatabaseDSN)
	}
}

func prepareBackendWorkspace(t *testing.T, files map[string]string) {
	t.Helper()

	workspaceDir := t.TempDir()
	backendDir := filepath.Join(workspaceDir, "backend")
	if err := os.MkdirAll(backendDir, 0o755); err != nil {
		t.Fatalf("create backend dir: %v", err)
	}

	for name, contents := range files {
		filePath := filepath.Join(workspaceDir, name)
		if err := os.WriteFile(filePath, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	currentDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(backendDir); err != nil {
		t.Fatalf("chdir %s: %v", backendDir, err)
	}

	t.Cleanup(func() {
		if err := os.Chdir(currentDir); err != nil {
			t.Fatalf("restore working dir: %v", err)
		}
	})
}

func resetConfigEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"APP_ENV",
		"APP_ADDR",
		"APP_PUBLIC_BASE_URL",
		"DB_DSN",
		"DB_MAX_OPEN_CONNS",
		"DB_MAX_IDLE_CONNS",
		"DB_CONN_MAX_LIFETIME_MINUTES",
		"APP_STORAGE_ROOT",
		"APP_MAX_UPLOAD_SIZE_BYTES",
		"APP_MAX_MULTIPART_MEMORY_BYTES",
		"APP_MULTIPART_THRESHOLD_BYTES",
		"APP_CHUNK_SIZE_BYTES",
		"APP_UPLOAD_SESSION_TTL_SECONDS",
		"APP_UPLOAD_CHUNK_CACHE_TTL_SECONDS",
		"APP_RATE_LIMIT_RPS",
		"APP_RATE_LIMIT_BURST",
		"APP_BEARER_TOKENS",
		"APP_SIGNING_SECRET",
		"APP_DEFAULT_SIGNED_URL_TTL_SECONDS",
		"APP_MAX_SIGNED_URL_TTL_SECONDS",
		"APP_READ_HEADER_TIMEOUT_SECONDS",
		"APP_SHUTDOWN_TIMEOUT_SECONDS",
	} {
		value, ok := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}

		key := key
		t.Cleanup(func() {
			if ok {
				if err := os.Setenv(key, value); err != nil {
					t.Fatalf("restore %s: %v", key, err)
				}
				return
			}
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("cleanup unset %s: %v", key, err)
			}
		})
	}
}
