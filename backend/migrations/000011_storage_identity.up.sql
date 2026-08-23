ALTER TABLE system_storage_quotas
    ADD COLUMN storage_id VARCHAR(36) NULL AFTER reconciled_at;

UPDATE system_storage_quotas
SET reconciled_at = NULL
WHERE id = 1;
