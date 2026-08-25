CREATE TABLE buckets (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY udx_buckets_name (name)
);

CREATE TABLE objects (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    bucket_name VARCHAR(128) NOT NULL,
    object_key VARCHAR(512) NOT NULL,
    original_filename VARCHAR(255) NOT NULL,
    storage_path VARCHAR(512) NOT NULL,
    size BIGINT NOT NULL,
    content_type VARCHAR(255) NOT NULL,
    etag VARCHAR(64) NOT NULL,
    file_fingerprint VARCHAR(64) NULL,
    visibility VARCHAR(16) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY udx_objects_bucket_key (bucket_name, object_key),
    KEY idx_objects_bucket_created (bucket_name, created_at DESC, id DESC),
    KEY idx_objects_bucket_key (bucket_name, object_key),
    KEY idx_objects_bucket_fingerprint (bucket_name, file_fingerprint),
    CONSTRAINT fk_objects_bucket_name
        FOREIGN KEY (bucket_name) REFERENCES buckets(name)
            ON UPDATE CASCADE
            ON DELETE CASCADE
);

CREATE TABLE sites (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    bucket_name VARCHAR(128) NOT NULL,
    root_prefix VARCHAR(512) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    index_document VARCHAR(255) NOT NULL DEFAULT 'index.html',
    error_document VARCHAR(255) NOT NULL DEFAULT '',
    spa_fallback BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_sites_bucket_name (bucket_name),
    CONSTRAINT fk_sites_bucket_name
        FOREIGN KEY (bucket_name) REFERENCES buckets(name)
            ON UPDATE CASCADE
            ON DELETE CASCADE
);

CREATE TABLE site_domains (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    site_id BIGINT UNSIGNED NOT NULL,
    domain VARCHAR(255) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY udx_site_domains_domain (domain),
    KEY idx_site_domains_site_id (site_id),
    CONSTRAINT fk_site_domains_site_id
        FOREIGN KEY (site_id) REFERENCES sites(id)
            ON UPDATE CASCADE
            ON DELETE CASCADE
);

CREATE TABLE system_storage_quotas (
    id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
    max_bytes BIGINT UNSIGNED NOT NULL,
    used_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0,
    reserved_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0,
    reconciled_at DATETIME(3) NULL,
    storage_id VARCHAR(36) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
);

INSERT INTO system_storage_quotas (id, max_bytes, used_bytes, reserved_bytes)
VALUES (1, 10737418240, 0, 0);

CREATE TABLE recycle_bin_objects (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    delete_group_id CHAR(36) NOT NULL,
    bucket_name VARCHAR(128) NOT NULL,
    object_key VARCHAR(512) NOT NULL,
    original_filename VARCHAR(255) NOT NULL,
    storage_path VARCHAR(512) NOT NULL,
    size BIGINT NOT NULL,
    content_type VARCHAR(255) NOT NULL,
    etag VARCHAR(64) NOT NULL,
    file_fingerprint VARCHAR(64) NULL,
    visibility VARCHAR(16) NOT NULL,
    created_at TIMESTAMP(6) NOT NULL,
    deleted_at TIMESTAMP(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_recycle_bin_objects_deleted (deleted_at DESC, id DESC),
    KEY idx_recycle_bin_objects_bucket (bucket_name),
    KEY idx_recycle_bin_objects_storage_path (storage_path),
    KEY idx_recycle_bin_objects_delete_group (delete_group_id),
    CONSTRAINT fk_recycle_bin_objects_bucket
        FOREIGN KEY (bucket_name) REFERENCES buckets(name)
            ON UPDATE CASCADE
            ON DELETE CASCADE
);

CREATE TABLE storage_blobs (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    storage_path VARCHAR(512) NOT NULL,
    staging_path VARCHAR(512) NULL,
    size BIGINT UNSIGNED NOT NULL DEFAULT 0,
    ref_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL,
    staging_lease_expires_at DATETIME(6) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY udx_storage_blobs_storage_path (storage_path),
    KEY idx_storage_blobs_status_created (status, created_at, id),
    KEY idx_storage_blobs_staging_lease (status, staging_lease_expires_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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
