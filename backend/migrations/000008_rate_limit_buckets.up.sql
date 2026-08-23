CREATE TABLE rate_limit_buckets (
    key_hash BINARY(32) NOT NULL PRIMARY KEY,
    tokens DOUBLE NOT NULL,
    last_refill_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    last_allowed BOOLEAN NOT NULL,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    KEY idx_rate_limit_buckets_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE rate_limit_capacity (
    id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    entry_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
    expired_evictions BIGINT UNSIGNED NOT NULL DEFAULT 0,
    capacity_rejections BIGINT UNSIGNED NOT NULL DEFAULT 0,
    CONSTRAINT chk_rate_limit_capacity_singleton CHECK (id = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO rate_limit_capacity (id) VALUES (1);
