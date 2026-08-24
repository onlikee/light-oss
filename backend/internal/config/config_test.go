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
			"APP_RATE_LIMIT_IP_RPS=31",
			"APP_RATE_LIMIT_IP_BURST=62",
			"APP_RATE_LIMIT_MANAGEMENT_RPS=9",
			"APP_RATE_LIMIT_MANAGEMENT_BURST=18",
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
	if cfg.StorageMode != StorageModeLocal {
		t.Fatalf("expected local storage mode by default, got %q", cfg.StorageMode)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Fatalf("expected forwarded headers to be untrusted by default, got %v", cfg.TrustedProxies)
	}
	if cfg.RateLimitBackend != "local" {
		t.Fatalf("expected local rate limit backend by default, got %q", cfg.RateLimitBackend)
	}
	if cfg.RateLimitPublicRPS != cfg.RateLimitIPRPS || cfg.RateLimitPublicBurst != cfg.RateLimitIPBurst {
		t.Fatal("expected public download defaults to inherit the IP budget")
	}
	if cfg.RateLimitManagementRPS != 9 || cfg.RateLimitManagementBurst != 18 {
		t.Fatal("expected management rate limit to use its explicit budget")
	}
	if cfg.RateLimitUploadRPS != 5 || cfg.RateLimitUploadBurst != 10 ||
		cfg.RateLimitSignRPS != 5 || cfg.RateLimitSignBurst != 10 ||
		cfg.RateLimitHealthRPS != 5 || cfg.RateLimitHealthBurst != 10 {
		t.Fatal("expected protected route classes to use independent defaults")
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

func TestLoadReadsRateLimitAndTrustedProxySettings(t *testing.T) {
	resetConfigEnv(t)
	prepareBackendWorkspace(t, map[string]string{
		".env": strings.Join([]string{
			"DB_DSN=test-dsn",
			"APP_STORAGE_ROOT=./storage",
			"APP_BEARER_TOKENS=test-token",
			"APP_SIGNING_SECRET=test-secret",
			"APP_RATE_LIMIT_BACKEND=mysql",
			"APP_RATE_LIMIT_IP_RPS=25.5",
			"APP_RATE_LIMIT_IP_BURST=50",
			"APP_RATE_LIMIT_MANAGEMENT_RPS=7.5",
			"APP_RATE_LIMIT_MANAGEMENT_BURST=15",
			"APP_RATE_LIMIT_PUBLIC_RPS=30",
			"APP_RATE_LIMIT_PUBLIC_BURST=60",
			"APP_RATE_LIMIT_UPLOAD_RPS=3",
			"APP_RATE_LIMIT_UPLOAD_BURST=6",
			"APP_RATE_LIMIT_SIGN_RPS=8",
			"APP_RATE_LIMIT_SIGN_BURST=16",
			"APP_RATE_LIMIT_HEALTH_RPS=4",
			"APP_RATE_LIMIT_HEALTH_BURST=8",
			"APP_RATE_LIMIT_CACHE_TTL_SECONDS=120",
			"APP_RATE_LIMIT_CACHE_MAX_ENTRIES=256",
			"APP_TRUSTED_PROXIES=10.0.0.10, 192.168.0.0/24",
			"",
		}, "\n"),
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.RateLimitIPRPS != 25.5 || cfg.RateLimitIPBurst != 50 {
		t.Fatalf("unexpected IP rate limit: rps=%v burst=%d", cfg.RateLimitIPRPS, cfg.RateLimitIPBurst)
	}
	if cfg.RateLimitBackend != "mysql" {
		t.Fatalf("unexpected rate limit backend: %q", cfg.RateLimitBackend)
	}
	if cfg.RateLimitManagementRPS != 7.5 || cfg.RateLimitManagementBurst != 15 {
		t.Fatalf("unexpected management rate limit: rps=%v burst=%d", cfg.RateLimitManagementRPS, cfg.RateLimitManagementBurst)
	}
	if cfg.RateLimitPublicRPS != 30 || cfg.RateLimitPublicBurst != 60 {
		t.Fatalf("unexpected public rate limit: rps=%v burst=%d", cfg.RateLimitPublicRPS, cfg.RateLimitPublicBurst)
	}
	if cfg.RateLimitUploadRPS != 3 || cfg.RateLimitUploadBurst != 6 {
		t.Fatalf("unexpected upload rate limit: rps=%v burst=%d", cfg.RateLimitUploadRPS, cfg.RateLimitUploadBurst)
	}
	if cfg.RateLimitSignRPS != 8 || cfg.RateLimitSignBurst != 16 {
		t.Fatalf("unexpected sign rate limit: rps=%v burst=%d", cfg.RateLimitSignRPS, cfg.RateLimitSignBurst)
	}
	if cfg.RateLimitHealthRPS != 4 || cfg.RateLimitHealthBurst != 8 {
		t.Fatalf("unexpected health rate limit: rps=%v burst=%d", cfg.RateLimitHealthRPS, cfg.RateLimitHealthBurst)
	}
	if cfg.RateLimitCacheTTLSeconds != 120 || cfg.RateLimitCacheMaxEntries != 256 {
		t.Fatalf("unexpected rate limit cache settings: ttl=%d max=%d", cfg.RateLimitCacheTTLSeconds, cfg.RateLimitCacheMaxEntries)
	}
	if got := strings.Join(cfg.TrustedProxies, ","); got != "10.0.0.10,192.168.0.0/24" {
		t.Fatalf("unexpected trusted proxies: %q", got)
	}
}

func TestLoadIgnoresRemovedRateLimitAliases(t *testing.T) {
	resetConfigEnv(t)
	prepareBackendWorkspace(t, map[string]string{
		".env": strings.Join([]string{
			"DB_DSN=test-dsn",
			"APP_STORAGE_ROOT=./storage",
			"APP_BEARER_TOKENS=test-token",
			"APP_SIGNING_SECRET=test-secret",
			"APP_RATE_LIMIT_RPS=99",
			"APP_RATE_LIMIT_BURST=199",
			"",
		}, "\n"),
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.RateLimitManagementRPS != 5 || cfg.RateLimitManagementBurst != 10 {
		t.Fatalf(
			"removed aliases changed management defaults: rps=%v burst=%d",
			cfg.RateLimitManagementRPS,
			cfg.RateLimitManagementBurst,
		)
	}
}

func TestLoadRejectsTrustingAllProxies(t *testing.T) {
	resetConfigEnv(t)
	prepareBackendWorkspace(t, map[string]string{
		".env": strings.Join([]string{
			"DB_DSN=test-dsn",
			"APP_STORAGE_ROOT=./storage",
			"APP_BEARER_TOKENS=test-token",
			"APP_SIGNING_SECRET=test-secret",
			"APP_TRUSTED_PROXIES=0.0.0.0/0",
			"",
		}, "\n"),
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load() to reject a trust-all proxy range")
	}
	if !strings.Contains(err.Error(), "must not trust all addresses") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsUnknownRateLimitBackend(t *testing.T) {
	resetConfigEnv(t)
	prepareBackendWorkspace(t, map[string]string{
		".env": strings.Join([]string{
			"DB_DSN=test-dsn",
			"APP_STORAGE_ROOT=./storage",
			"APP_BEARER_TOKENS=test-token",
			"APP_SIGNING_SECRET=test-secret",
			"APP_RATE_LIMIT_BACKEND=redis",
			"",
		}, "\n"),
	})

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "APP_RATE_LIMIT_BACKEND must be local or mysql") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadReadsAndValidatesStorageMode(t *testing.T) {
	resetConfigEnv(t)
	prepareBackendWorkspace(t, map[string]string{
		".env": strings.Join([]string{
			"DB_DSN=test-dsn",
			"APP_STORAGE_MODE=SHARED-FILESYSTEM",
			"APP_STORAGE_ROOT=./storage",
			"APP_BEARER_TOKENS=test-token",
			"APP_SIGNING_SECRET=test-secret",
			"",
		}, "\n"),
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.StorageMode != StorageModeSharedFilesystem {
		t.Fatalf("storage mode = %q, want %q", cfg.StorageMode, StorageModeSharedFilesystem)
	}

	t.Setenv("APP_STORAGE_MODE", "object-storage")
	_, err = Load()
	if err == nil || !strings.Contains(err.Error(), "APP_STORAGE_MODE must be local or shared-filesystem") {
		t.Fatalf("Load() error = %v, want storage mode validation error", err)
	}
}

func TestLoadRejectsInvalidDatabasePoolSettings(t *testing.T) {
	tests := []struct {
		name        string
		setting     string
		wantMessage string
	}{
		{name: "zero max open", setting: "DB_MAX_OPEN_CONNS=0", wantMessage: "DB_MAX_OPEN_CONNS must be greater than zero"},
		{name: "negative max idle", setting: "DB_MAX_IDLE_CONNS=-1", wantMessage: "DB_MAX_IDLE_CONNS must not be negative"},
		{name: "idle exceeds open", setting: "DB_MAX_IDLE_CONNS=11", wantMessage: "DB_MAX_IDLE_CONNS must not exceed DB_MAX_OPEN_CONNS"},
		{name: "zero max lifetime", setting: "DB_CONN_MAX_LIFETIME_MINUTES=0", wantMessage: "DB_CONN_MAX_LIFETIME_MINUTES must be greater than zero"},
		{name: "zero connect timeout", setting: "DB_CONNECT_TIMEOUT_SECONDS=0", wantMessage: "DB_CONNECT_TIMEOUT_SECONDS must be greater than zero"},
		{name: "zero read timeout", setting: "DB_READ_TIMEOUT_SECONDS=0", wantMessage: "DB_READ_TIMEOUT_SECONDS must be greater than zero"},
		{name: "zero write timeout", setting: "DB_WRITE_TIMEOUT_SECONDS=0", wantMessage: "DB_WRITE_TIMEOUT_SECONDS must be greater than zero"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetConfigEnv(t)
			prepareBackendWorkspace(t, map[string]string{
				".env": strings.Join([]string{
					"DB_DSN=test-dsn",
					"APP_STORAGE_ROOT=./storage",
					"APP_BEARER_TOKENS=test-token",
					"APP_SIGNING_SECRET=test-secret",
					tt.setting,
					"",
				}, "\n"),
			})

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("Load() error = %v, want message containing %q", err, tt.wantMessage)
			}
		})
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
		"DB_DSN",
		"DB_MAX_OPEN_CONNS",
		"DB_MAX_IDLE_CONNS",
		"DB_CONN_MAX_LIFETIME_MINUTES",
		"DB_CONNECT_TIMEOUT_SECONDS",
		"DB_READ_TIMEOUT_SECONDS",
		"DB_WRITE_TIMEOUT_SECONDS",
		"APP_STORAGE_MODE",
		"APP_STORAGE_ROOT",
		"APP_STORAGE_STAGING_TTL_SECONDS",
		"APP_MAX_UPLOAD_SIZE_BYTES",
		"APP_MAX_MULTIPART_MEMORY_BYTES",
		"APP_CHUNK_SIZE_BYTES",
		"APP_RATE_LIMIT_BACKEND",
		"APP_RATE_LIMIT_IP_RPS",
		"APP_RATE_LIMIT_IP_BURST",
		"APP_RATE_LIMIT_MANAGEMENT_RPS",
		"APP_RATE_LIMIT_MANAGEMENT_BURST",
		"APP_RATE_LIMIT_PUBLIC_RPS",
		"APP_RATE_LIMIT_PUBLIC_BURST",
		"APP_RATE_LIMIT_UPLOAD_RPS",
		"APP_RATE_LIMIT_UPLOAD_BURST",
		"APP_RATE_LIMIT_SIGN_RPS",
		"APP_RATE_LIMIT_SIGN_BURST",
		"APP_RATE_LIMIT_HEALTH_RPS",
		"APP_RATE_LIMIT_HEALTH_BURST",
		"APP_RATE_LIMIT_CACHE_TTL_SECONDS",
		"APP_RATE_LIMIT_CACHE_MAX_ENTRIES",
		"APP_TRUSTED_PROXIES",
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
