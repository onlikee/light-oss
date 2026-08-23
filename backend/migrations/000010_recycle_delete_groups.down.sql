ALTER TABLE recycle_bin_objects
    DROP INDEX idx_recycle_bin_objects_delete_group,
    DROP COLUMN delete_group_id;
