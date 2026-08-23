# Legacy upload-session 表说明

当前后端运行时代码不再读写以下历史表：

- `upload_sessions`
- `upload_session_chunks`
- `upload_chunk_blobs`
- `folder_upload_sessions`
- `folder_upload_entries`

这些表由历史迁移 `000002_upload_sessions` 创建。为保证已经执行过该迁移的数据库仍能按既有版本链升级，`000002` 的 up/down 文件继续保留，不回写或删除。

当前上传流程使用 `storage_blobs`、原子配额预留和 staging/cleanup 生命周期。历史表此时仅作为数据库兼容遗留存在，不代表仍受运行时维护。

后续若要删除这些表，必须新增独立的顺序迁移，并在迁移前确认无需保留历史数据，同时提供回滚和部署兼容方案。
