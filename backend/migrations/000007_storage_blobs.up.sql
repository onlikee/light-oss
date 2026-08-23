ALTER TABLE system_storage_quotas
    ADD COLUMN used_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER max_bytes,
    ADD COLUMN reserved_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER used_bytes,
    ADD COLUMN reconciled_at DATETIME(3) NULL AFTER reserved_bytes;

CREATE TABLE storage_blobs (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    storage_path VARCHAR(512) NOT NULL,
    staging_path VARCHAR(512) NULL,
    size BIGINT UNSIGNED NOT NULL DEFAULT 0,
    ref_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY udx_storage_blobs_storage_path (storage_path),
    KEY idx_storage_blobs_status_created (status, created_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO storage_blobs (id, storage_path, size, ref_count, status)
SELECT
    UUID(),
    referenced.storage_path,
    CAST(MAX(referenced.size) AS UNSIGNED),
    COUNT(*),
    'active'
FROM (
    SELECT storage_path, size
    FROM objects
    WHERE storage_path <> ''
    UNION ALL
    SELECT storage_path, size
    FROM recycle_bin_objects
    WHERE storage_path <> ''
) AS referenced
GROUP BY referenced.storage_path;

UPDATE system_storage_quotas
SET used_bytes = COALESCE((SELECT SUM(size) FROM storage_blobs), 0)
WHERE id = 1;

CREATE TABLE storage_cleanup_jobs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    blob_id VARCHAR(36) NOT NULL,
    storage_path VARCHAR(512) NOT NULL,
    retry_count INT UNSIGNED NOT NULL DEFAULT 0,
    next_retry_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    last_error TEXT NOT NULL,
    lease_owner VARCHAR(128) NULL,
    lease_expires_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY udx_storage_cleanup_jobs_blob_id (blob_id),
    KEY idx_storage_cleanup_jobs_next_retry_at (next_retry_at),
    KEY idx_storage_cleanup_jobs_lease_expires_at (lease_expires_at),
    CONSTRAINT fk_storage_cleanup_jobs_blob_id
        FOREIGN KEY (blob_id) REFERENCES storage_blobs(id)
            ON UPDATE CASCADE
            ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
