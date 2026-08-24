ALTER TABLE objects
    ADD CONSTRAINT chk_objects_no_legacy_deleted CHECK (is_deleted = FALSE);

ALTER TABLE objects
    DROP CHECK chk_objects_no_legacy_deleted,
    DROP KEY idx_objects_bucket_created,
    DROP KEY idx_objects_bucket_key,
    DROP KEY idx_objects_bucket_fingerprint,
    DROP COLUMN is_deleted,
    ADD KEY idx_objects_bucket_created (bucket_name, created_at DESC, id DESC),
    ADD KEY idx_objects_bucket_key (bucket_name, object_key),
    ADD KEY idx_objects_bucket_fingerprint (bucket_name, file_fingerprint);

DROP TABLE IF EXISTS folder_upload_entries;
DROP TABLE IF EXISTS folder_upload_sessions;
DROP TABLE IF EXISTS upload_chunk_blobs;
DROP TABLE IF EXISTS upload_session_chunks;
DROP TABLE IF EXISTS upload_sessions;
