# Legacy schema 清理说明

当前后端运行时代码不再读写以下历史表：

- `upload_sessions`
- `upload_session_chunks`
- `upload_chunk_blobs`
- `folder_upload_sessions`
- `folder_upload_entries`

这些表由历史迁移 `000002_upload_sessions` 创建。为保证已经执行过该迁移的数据库仍能按既有版本链升级，`000002` 的 up/down 文件继续保留，不回写或删除。

当前上传流程使用 `storage_blobs`、原子配额预留和 staging/cleanup 生命周期。`000012_remove_legacy_schema` 会在升级时删除这些空闲历史表，同时移除已被 `recycle_bin_objects` 取代的 `objects.is_deleted` 列及其运行时查询分支。

`000012` 的 down migration 可以恢复旧表结构和 `is_deleted` 列，但不会恢复升级前已经删除的历史上传会话数据。部署前如需保留这些历史数据，应先完成独立备份；应用新旧版本不可混跑。
