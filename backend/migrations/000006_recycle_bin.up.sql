CREATE TABLE recycle_bin_objects (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
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
    CONSTRAINT fk_recycle_bin_objects_bucket FOREIGN KEY (bucket_name) REFERENCES buckets(name) ON UPDATE CASCADE ON DELETE CASCADE
) ENGINE=InnoDB;

INSERT INTO recycle_bin_objects (
    bucket_name,
    object_key,
    original_filename,
    storage_path,
    size,
    content_type,
    etag,
    file_fingerprint,
    visibility,
    created_at,
    deleted_at
)
SELECT
    bucket_name,
    object_key,
    original_filename,
    storage_path,
    size,
    content_type,
    etag,
    file_fingerprint,
    visibility,
    created_at,
    updated_at
FROM objects
WHERE is_deleted = TRUE;

DELETE FROM objects WHERE is_deleted = TRUE;
