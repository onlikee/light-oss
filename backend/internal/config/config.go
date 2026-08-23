package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/spf13/viper"
)

const (
	StorageModeLocal            = "local"
	StorageModeSharedFilesystem = "shared-filesystem"
	RateLimitBackendLocal       = "local"
	RateLimitBackendMySQL       = "mysql"
)

type Config struct {
	AppEnv                         string
	AppAddr                        string
	DatabaseDSN                    string
	DatabaseMaxOpenConns           int
	DatabaseMaxIdleConns           int
	DatabaseConnMaxLifetimeMinutes int
	DatabaseConnectTimeoutSeconds  int
	DatabaseReadTimeoutSeconds     int
	DatabaseWriteTimeoutSeconds    int
	StorageMode                    string
	StorageRoot                    string
	StorageStagingTTLSeconds       int64
	MaxUploadSizeBytes             int64
	MaxMultipartMemoryBytes        int64
	ChunkSizeBytes                 int64
	RateLimitBackend               string
	RateLimitIPRPS                 float64
	RateLimitIPBurst               int
	RateLimitRPS                   float64
	RateLimitBurst                 int
	RateLimitPublicRPS             float64
	RateLimitPublicBurst           int
	RateLimitUploadRPS             float64
	RateLimitUploadBurst           int
	RateLimitSignRPS               float64
	RateLimitSignBurst             int
	RateLimitHealthRPS             float64
	RateLimitHealthBurst           int
	RateLimitCacheTTLSeconds       int64
	RateLimitCacheMaxEntries       int
	TrustedProxies                 []string
	BearerTokens                   []string
	SigningSecret                  string
	DefaultSignedURLTTLSeconds     int64
	MaxSignedURLTTLSeconds         int64
	ReadHeaderTimeoutSeconds       int
	ShutdownTimeoutSeconds         int
}

func Load() (Config, error) {
	v := viper.New()
	v.SetConfigType("env")
	if err := loadLocalEnv(v); err != nil {
		return Config{}, err
	}
	v.AutomaticEnv()

	v.SetDefault("APP_ENV", "development")
	v.SetDefault("APP_ADDR", ":8080")
	v.SetDefault("DB_DSN", "root:123456@tcp(localhost:3306)/light-oss?charset=utf8mb4&parseTime=True&loc=UTC&multiStatements=true")
	v.SetDefault("DB_MAX_OPEN_CONNS", 10)
	v.SetDefault("DB_MAX_IDLE_CONNS", 5)
	v.SetDefault("DB_CONN_MAX_LIFETIME_MINUTES", 30)
	v.SetDefault("DB_CONNECT_TIMEOUT_SECONDS", 5)
	v.SetDefault("DB_READ_TIMEOUT_SECONDS", 300)
	v.SetDefault("DB_WRITE_TIMEOUT_SECONDS", 30)
	v.SetDefault("APP_STORAGE_MODE", StorageModeLocal)
	v.SetDefault("APP_STORAGE_ROOT", `.\light-oss-data\storage`)
	v.SetDefault("APP_STORAGE_STAGING_TTL_SECONDS", int64(86400))
	v.SetDefault("APP_MAX_UPLOAD_SIZE_BYTES", int64(50*1024*1024))
	v.SetDefault("APP_MAX_MULTIPART_MEMORY_BYTES", int64(8*1024*1024))
	v.SetDefault("APP_CHUNK_SIZE_BYTES", int64(8*1024*1024))
	v.SetDefault("APP_RATE_LIMIT_BACKEND", RateLimitBackendLocal)
	v.SetDefault("APP_RATE_LIMIT_IP_RPS", 20.0)
	v.SetDefault("APP_RATE_LIMIT_IP_BURST", 40)
	v.SetDefault("APP_RATE_LIMIT_RPS", 5.0)
	v.SetDefault("APP_RATE_LIMIT_BURST", 10)
	v.SetDefault("APP_RATE_LIMIT_PUBLIC_RPS", v.GetFloat64("APP_RATE_LIMIT_IP_RPS"))
	v.SetDefault("APP_RATE_LIMIT_PUBLIC_BURST", v.GetInt("APP_RATE_LIMIT_IP_BURST"))
	v.SetDefault("APP_RATE_LIMIT_UPLOAD_RPS", v.GetFloat64("APP_RATE_LIMIT_RPS"))
	v.SetDefault("APP_RATE_LIMIT_UPLOAD_BURST", v.GetInt("APP_RATE_LIMIT_BURST"))
	v.SetDefault("APP_RATE_LIMIT_SIGN_RPS", v.GetFloat64("APP_RATE_LIMIT_RPS"))
	v.SetDefault("APP_RATE_LIMIT_SIGN_BURST", v.GetInt("APP_RATE_LIMIT_BURST"))
	v.SetDefault("APP_RATE_LIMIT_HEALTH_RPS", v.GetFloat64("APP_RATE_LIMIT_RPS"))
	v.SetDefault("APP_RATE_LIMIT_HEALTH_BURST", v.GetInt("APP_RATE_LIMIT_BURST"))
	v.SetDefault("APP_RATE_LIMIT_CACHE_TTL_SECONDS", int64(600))
	v.SetDefault("APP_RATE_LIMIT_CACHE_MAX_ENTRIES", 10000)
	v.SetDefault("APP_TRUSTED_PROXIES", "")
	v.SetDefault("APP_BEARER_TOKENS", "dev-token")
	v.SetDefault("APP_SIGNING_SECRET", "dev-signing-secret")
	v.SetDefault("APP_DEFAULT_SIGNED_URL_TTL_SECONDS", 300)
	v.SetDefault("APP_MAX_SIGNED_URL_TTL_SECONDS", 86400)
	v.SetDefault("APP_READ_HEADER_TIMEOUT_SECONDS", 10)
	v.SetDefault("APP_SHUTDOWN_TIMEOUT_SECONDS", 10)

	cfg := Config{
		AppEnv:                         strings.ToLower(v.GetString("APP_ENV")),
		AppAddr:                        v.GetString("APP_ADDR"),
		DatabaseDSN:                    v.GetString("DB_DSN"),
		DatabaseMaxOpenConns:           v.GetInt("DB_MAX_OPEN_CONNS"),
		DatabaseMaxIdleConns:           v.GetInt("DB_MAX_IDLE_CONNS"),
		DatabaseConnMaxLifetimeMinutes: v.GetInt("DB_CONN_MAX_LIFETIME_MINUTES"),
		DatabaseConnectTimeoutSeconds:  v.GetInt("DB_CONNECT_TIMEOUT_SECONDS"),
		DatabaseReadTimeoutSeconds:     v.GetInt("DB_READ_TIMEOUT_SECONDS"),
		DatabaseWriteTimeoutSeconds:    v.GetInt("DB_WRITE_TIMEOUT_SECONDS"),
		StorageMode:                    strings.ToLower(strings.TrimSpace(v.GetString("APP_STORAGE_MODE"))),
		StorageRoot:                    v.GetString("APP_STORAGE_ROOT"),
		StorageStagingTTLSeconds:       v.GetInt64("APP_STORAGE_STAGING_TTL_SECONDS"),
		MaxUploadSizeBytes:             v.GetInt64("APP_MAX_UPLOAD_SIZE_BYTES"),
		MaxMultipartMemoryBytes:        v.GetInt64("APP_MAX_MULTIPART_MEMORY_BYTES"),
		ChunkSizeBytes:                 v.GetInt64("APP_CHUNK_SIZE_BYTES"),
		RateLimitBackend:               strings.ToLower(strings.TrimSpace(v.GetString("APP_RATE_LIMIT_BACKEND"))),
		RateLimitIPRPS:                 v.GetFloat64("APP_RATE_LIMIT_IP_RPS"),
		RateLimitIPBurst:               v.GetInt("APP_RATE_LIMIT_IP_BURST"),
		RateLimitRPS:                   v.GetFloat64("APP_RATE_LIMIT_RPS"),
		RateLimitBurst:                 v.GetInt("APP_RATE_LIMIT_BURST"),
		RateLimitPublicRPS:             v.GetFloat64("APP_RATE_LIMIT_PUBLIC_RPS"),
		RateLimitPublicBurst:           v.GetInt("APP_RATE_LIMIT_PUBLIC_BURST"),
		RateLimitUploadRPS:             v.GetFloat64("APP_RATE_LIMIT_UPLOAD_RPS"),
		RateLimitUploadBurst:           v.GetInt("APP_RATE_LIMIT_UPLOAD_BURST"),
		RateLimitSignRPS:               v.GetFloat64("APP_RATE_LIMIT_SIGN_RPS"),
		RateLimitSignBurst:             v.GetInt("APP_RATE_LIMIT_SIGN_BURST"),
		RateLimitHealthRPS:             v.GetFloat64("APP_RATE_LIMIT_HEALTH_RPS"),
		RateLimitHealthBurst:           v.GetInt("APP_RATE_LIMIT_HEALTH_BURST"),
		RateLimitCacheTTLSeconds:       v.GetInt64("APP_RATE_LIMIT_CACHE_TTL_SECONDS"),
		RateLimitCacheMaxEntries:       v.GetInt("APP_RATE_LIMIT_CACHE_MAX_ENTRIES"),
		TrustedProxies:                 splitCSV(v.GetString("APP_TRUSTED_PROXIES")),
		BearerTokens:                   splitCSV(v.GetString("APP_BEARER_TOKENS")),
		SigningSecret:                  v.GetString("APP_SIGNING_SECRET"),
		DefaultSignedURLTTLSeconds:     v.GetInt64("APP_DEFAULT_SIGNED_URL_TTL_SECONDS"),
		MaxSignedURLTTLSeconds:         v.GetInt64("APP_MAX_SIGNED_URL_TTL_SECONDS"),
		ReadHeaderTimeoutSeconds:       v.GetInt("APP_READ_HEADER_TIMEOUT_SECONDS"),
		ShutdownTimeoutSeconds:         v.GetInt("APP_SHUTDOWN_TIMEOUT_SECONDS"),
	}

	switch {
	case cfg.DatabaseDSN == "":
		return Config{}, fmt.Errorf("DB_DSN is required")
	case cfg.DatabaseMaxOpenConns <= 0:
		return Config{}, fmt.Errorf("DB_MAX_OPEN_CONNS must be greater than zero")
	case cfg.DatabaseMaxIdleConns < 0:
		return Config{}, fmt.Errorf("DB_MAX_IDLE_CONNS must not be negative")
	case cfg.DatabaseMaxIdleConns > cfg.DatabaseMaxOpenConns:
		return Config{}, fmt.Errorf("DB_MAX_IDLE_CONNS must not exceed DB_MAX_OPEN_CONNS")
	case cfg.DatabaseConnMaxLifetimeMinutes <= 0:
		return Config{}, fmt.Errorf("DB_CONN_MAX_LIFETIME_MINUTES must be greater than zero")
	case cfg.DatabaseConnectTimeoutSeconds <= 0:
		return Config{}, fmt.Errorf("DB_CONNECT_TIMEOUT_SECONDS must be greater than zero")
	case cfg.DatabaseReadTimeoutSeconds <= 0:
		return Config{}, fmt.Errorf("DB_READ_TIMEOUT_SECONDS must be greater than zero")
	case cfg.DatabaseWriteTimeoutSeconds <= 0:
		return Config{}, fmt.Errorf("DB_WRITE_TIMEOUT_SECONDS must be greater than zero")
	case cfg.StorageMode != StorageModeLocal && cfg.StorageMode != StorageModeSharedFilesystem:
		return Config{}, fmt.Errorf("APP_STORAGE_MODE must be local or shared-filesystem")
	case cfg.StorageRoot == "":
		return Config{}, fmt.Errorf("APP_STORAGE_ROOT is required")
	case cfg.StorageStagingTTLSeconds <= 0:
		return Config{}, fmt.Errorf("APP_STORAGE_STAGING_TTL_SECONDS must be greater than zero")
	case len(cfg.BearerTokens) == 0:
		return Config{}, fmt.Errorf("APP_BEARER_TOKENS is required")
	case cfg.SigningSecret == "":
		return Config{}, fmt.Errorf("APP_SIGNING_SECRET is required")
	case cfg.MaxUploadSizeBytes <= 0:
		return Config{}, fmt.Errorf("APP_MAX_UPLOAD_SIZE_BYTES must be greater than zero")
	case cfg.ChunkSizeBytes <= 0:
		return Config{}, fmt.Errorf("APP_CHUNK_SIZE_BYTES must be greater than zero")
	case cfg.ChunkSizeBytes > cfg.MaxUploadSizeBytes:
		return Config{}, fmt.Errorf("APP_CHUNK_SIZE_BYTES must not exceed APP_MAX_UPLOAD_SIZE_BYTES")
	case cfg.RateLimitBackend != RateLimitBackendLocal && cfg.RateLimitBackend != RateLimitBackendMySQL:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_BACKEND must be local or mysql")
	case cfg.RateLimitIPRPS <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_IP_RPS must be greater than zero")
	case cfg.RateLimitIPBurst <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_IP_BURST must be greater than zero")
	case cfg.RateLimitRPS <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_RPS must be greater than zero")
	case cfg.RateLimitBurst <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_BURST must be greater than zero")
	case cfg.RateLimitPublicRPS <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_PUBLIC_RPS must be greater than zero")
	case cfg.RateLimitPublicBurst <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_PUBLIC_BURST must be greater than zero")
	case cfg.RateLimitUploadRPS <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_UPLOAD_RPS must be greater than zero")
	case cfg.RateLimitUploadBurst <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_UPLOAD_BURST must be greater than zero")
	case cfg.RateLimitSignRPS <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_SIGN_RPS must be greater than zero")
	case cfg.RateLimitSignBurst <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_SIGN_BURST must be greater than zero")
	case cfg.RateLimitHealthRPS <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_HEALTH_RPS must be greater than zero")
	case cfg.RateLimitHealthBurst <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_HEALTH_BURST must be greater than zero")
	case cfg.RateLimitCacheTTLSeconds <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_CACHE_TTL_SECONDS must be greater than zero")
	case cfg.RateLimitCacheMaxEntries <= 0:
		return Config{}, fmt.Errorf("APP_RATE_LIMIT_CACHE_MAX_ENTRIES must be greater than zero")
	}

	if err := validateTrustedProxies(cfg.TrustedProxies); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func validateTrustedProxies(proxies []string) error {
	for _, proxy := range proxies {
		if net.ParseIP(proxy) != nil {
			continue
		}

		_, network, err := net.ParseCIDR(proxy)
		if err != nil {
			return fmt.Errorf("APP_TRUSTED_PROXIES contains invalid IP or CIDR %q", proxy)
		}
		ones, _ := network.Mask.Size()
		if ones == 0 {
			return fmt.Errorf("APP_TRUSTED_PROXIES must not trust all addresses")
		}
	}

	return nil
}

func loadLocalEnv(v *viper.Viper) error {
	for _, path := range []string{".env.personal", "../.env.personal", ".env", "../.env"} {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat %s: %w", path, err)
		}

		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		return nil
	}

	return nil
}

func splitCSV(input string) []string {
	if input == "" {
		return nil
	}

	parts := strings.Split(input, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}

	return values
}
