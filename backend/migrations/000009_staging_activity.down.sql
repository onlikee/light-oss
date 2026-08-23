ALTER TABLE storage_blobs
    DROP INDEX idx_storage_blobs_staging_lease,
    DROP COLUMN staging_lease_expires_at;
