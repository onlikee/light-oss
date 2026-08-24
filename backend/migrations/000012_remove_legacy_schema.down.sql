ALTER TABLE objects
    DROP KEY idx_objects_bucket_created,
    DROP KEY idx_objects_bucket_key,
    DROP KEY idx_objects_bucket_fingerprint,
    ADD COLUMN is_deleted BOOLEAN NOT NULL DEFAULT FALSE AFTER visibility,
    ADD KEY idx_objects_bucket_created (bucket_name, is_deleted, created_at DESC, id DESC),
    ADD KEY idx_objects_bucket_key (bucket_name, is_deleted, object_key),
    ADD KEY idx_objects_bucket_fingerprint (bucket_name, is_deleted, file_fingerprint);

CREATE TABLE upload_sessions (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    owner_scope VARCHAR(64) NOT NULL,
    bucket_name VARCHAR(128) NOT NULL,
    object_key VARCHAR(512) NOT NULL,
    original_filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(255) NOT NULL,
    visibility VARCHAR(16) NOT NULL,
    size BIGINT NOT NULL,
    file_fingerprint VARCHAR(64) NOT NULL,
    mode VARCHAR(16) NOT NULL,
    status VARCHAR(32) NOT NULL,
    chunk_size_bytes BIGINT NOT NULL,
    total_chunks INT NOT NULL,
    folder_upload_session_id VARCHAR(36) NULL,
    folder_entry_id VARCHAR(36) NULL,
    staging_path VARCHAR(512) NULL,
    staged_size BIGINT NOT NULL DEFAULT 0,
    staged_etag VARCHAR(64) NULL,
    reused_object BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at DATETIME(3) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_upload_sessions_owner_status (owner_scope, status),
    KEY idx_upload_sessions_owner_lookup (owner_scope, bucket_name, object_key, file_fingerprint),
    KEY idx_upload_sessions_expires_at (expires_at),
    KEY idx_upload_sessions_folder_upload_session_id (folder_upload_session_id),
    KEY idx_upload_sessions_folder_entry_id (folder_entry_id)
);

CREATE TABLE upload_session_chunks (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    upload_session_id VARCHAR(36) NOT NULL,
    chunk_index INT NOT NULL,
    chunk_size BIGINT NOT NULL,
    chunk_sha256 VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY udx_upload_session_chunks_session_index (upload_session_id, chunk_index),
    KEY idx_upload_session_chunks_sha (chunk_sha256),
    KEY idx_upload_session_chunks_upload_session_id (upload_session_id)
);

CREATE TABLE upload_chunk_blobs (
    sha256 VARCHAR(64) NOT NULL PRIMARY KEY,
    storage_path VARCHAR(512) NOT NULL,
    size BIGINT NOT NULL,
    expires_at DATETIME(3) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_upload_chunk_blobs_expires_at (expires_at)
);

CREATE TABLE folder_upload_sessions (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    owner_scope VARCHAR(64) NOT NULL,
    bucket_name VARCHAR(128) NOT NULL,
    prefix VARCHAR(512) NOT NULL,
    visibility VARCHAR(16) NOT NULL,
    batch_fingerprint VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    expires_at DATETIME(3) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_folder_upload_sessions_owner_lookup (owner_scope, bucket_name, prefix, batch_fingerprint),
    KEY idx_folder_upload_sessions_status (status),
    KEY idx_folder_upload_sessions_expires_at (expires_at)
);

CREATE TABLE folder_upload_entries (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    folder_upload_session_id VARCHAR(36) NOT NULL,
    relative_path VARCHAR(512) NOT NULL,
    object_key VARCHAR(512) NOT NULL,
    original_filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(255) NOT NULL,
    size BIGINT NOT NULL,
    file_fingerprint VARCHAR(64) NOT NULL,
    mode VARCHAR(16) NOT NULL,
    status VARCHAR(32) NOT NULL,
    upload_session_id VARCHAR(36) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY udx_folder_upload_entries_session_path (folder_upload_session_id, relative_path),
    KEY idx_folder_upload_entries_folder_upload_session_id (folder_upload_session_id),
    KEY idx_folder_upload_entries_upload_session_id (upload_session_id)
);
