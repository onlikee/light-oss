ALTER TABLE storage_blobs
    ADD COLUMN staging_lease_expires_at DATETIME(6) NULL AFTER status,
    ADD KEY idx_storage_blobs_staging_lease (status, staging_lease_expires_at, id);

UPDATE storage_blobs
SET staging_lease_expires_at = UTC_TIMESTAMP(6)
WHERE status = 'staging';
