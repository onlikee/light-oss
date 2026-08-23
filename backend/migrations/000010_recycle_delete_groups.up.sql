ALTER TABLE recycle_bin_objects
    ADD COLUMN delete_group_id CHAR(36) NULL AFTER id;

CREATE TEMPORARY TABLE recycle_delete_group_backfill (
    recycle_bin_object_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
    group_seed_id BIGINT UNSIGNED NOT NULL
) ENGINE=InnoDB;

INSERT INTO recycle_delete_group_backfill (recycle_bin_object_id, group_seed_id)
SELECT
    item.id,
    COALESCE((
        SELECT marker.id
        FROM recycle_bin_objects AS marker
        WHERE marker.bucket_name = item.bucket_name
          AND marker.deleted_at = item.deleted_at
          AND RIGHT(marker.object_key, CHAR_LENGTH('/.light-oss-folder')) = '/.light-oss-folder'
          AND LEFT(item.object_key, CHAR_LENGTH(marker.object_key) - CHAR_LENGTH('.light-oss-folder')) =
              LEFT(marker.object_key, CHAR_LENGTH(marker.object_key) - CHAR_LENGTH('.light-oss-folder'))
        ORDER BY CHAR_LENGTH(marker.object_key) ASC, marker.id DESC
        LIMIT 1
    ), item.id)
FROM recycle_bin_objects AS item;

UPDATE recycle_bin_objects AS item
JOIN recycle_delete_group_backfill AS backfill
    ON backfill.recycle_bin_object_id = item.id
SET item.delete_group_id = LOWER(CONCAT(
    SUBSTRING(SHA2(CONCAT('light-oss:recycle-delete-group:', backfill.group_seed_id), 256), 1, 8), '-',
    SUBSTRING(SHA2(CONCAT('light-oss:recycle-delete-group:', backfill.group_seed_id), 256), 9, 4), '-',
    '5', SUBSTRING(SHA2(CONCAT('light-oss:recycle-delete-group:', backfill.group_seed_id), 256), 14, 3), '-',
    '8', SUBSTRING(SHA2(CONCAT('light-oss:recycle-delete-group:', backfill.group_seed_id), 256), 18, 3), '-',
    SUBSTRING(SHA2(CONCAT('light-oss:recycle-delete-group:', backfill.group_seed_id), 256), 21, 12)
));

DROP TEMPORARY TABLE recycle_delete_group_backfill;

ALTER TABLE recycle_bin_objects
    MODIFY COLUMN delete_group_id CHAR(36) NOT NULL,
    ADD KEY idx_recycle_bin_objects_delete_group (delete_group_id);
