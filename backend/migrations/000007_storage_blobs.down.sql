DROP TABLE IF EXISTS storage_cleanup_jobs;
DROP TABLE IF EXISTS storage_blobs;

ALTER TABLE system_storage_quotas
    DROP COLUMN reconciled_at,
    DROP COLUMN reserved_bytes,
    DROP COLUMN used_bytes;
