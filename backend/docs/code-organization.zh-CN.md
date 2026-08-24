# 后端代码组织约定

后端保持单体部署和单 Go module，不为拆文件而引入额外 package。运行时依赖方向固定为：

```text
handler -> service -> repository -> MySQL
                   -> BlobStore  -> filesystem
```

同一领域内共享事务辅助函数时，优先在原 package 中按职责拆文件。只有形成可独立复用、依赖方向清晰的能力时，才新增子 package。

## Handler

- `router.go`：构造 Gin、全局中间件和限流器，不承载业务 handler。
- `routes.go`：集中注册路由，并按 Bucket、Explorer、Object、Recycle、Site、System 分组。
- `bucket_object.go`、`explorer.go`、`recycle_bin.go`、`site*.go`、`system.go`：分别负责请求解析、响应映射和调用 service。
- handler 不直接拼业务 SQL，也不拥有跨仓储事务。

## Service

- `object_service.go`：对象上传、读取、列表、删除和可见性等主用例。
- `object_service_helpers.go`：对象游标、Content-Type 规范化、回收站模型映射等共享辅助逻辑。
- `explorer.go`、`explorer_folder.go`、`explorer_cursor.go`、`explorer_path.go`：分别负责 Explorer 列表、文件夹命令、游标排序和路径规则。
- `recycle_bin_service.go`、`recycle_bin_actions.go`、`recycle_bin_grouping.go`：分别负责批量用例编排、单项恢复/永久删除和逻辑目录分组。
- `blob_lifecycle_service.go`、`blob_batch_lifecycle.go`、`blob_publish_lifecycle.go`、`blob_abort_lifecycle.go`：分别负责单 Blob staging、批量 staging、元数据发布和失败补偿。

service 负责业务事务边界。文件 I/O 在事务外完成；事务内只发布元数据、更新 Blob 台账和创建持久化清理任务。

## Repository

- `object_repository.go`：仓储类型、事务入口和创建/覆盖写入。
- `object_query_repository.go`、`object_command_repository.go`：对象查询与删除/更新命令。
- `storage_blob_repository.go`：Blob 仓储入口、advisory lock、staging 创建和基础查询。
- `storage_blob_ledger_repository.go`、`storage_blob_release_repository.go`：配额预留/激活与引用释放。
- `storage_blob_reconciliation_repository.go`、`storage_blob_audit_repository.go`：恢复状态迁移与全量台账一致性校验。
- `storage_cleanup_job_repository.go`：清理任务的入队、租约、失败重试和完成。

repository 只表达持久化操作，不返回 HTTP 错误。需要多个仓储原子协作的用例由 service 开启事务，并通过 `WithDB` 把同一事务传入各仓储。

## 测试文件

路由测试按 `security_health`、`object`、`object_batch`、`explorer`、`bucket_folder`、`recycle_bin`、`site`、`storage_quota` 等能力拆分。共享的 Router、SQLite、Blob 生命周期和 HTTP 构造辅助函数集中在 `router_test_support_test.go`，避免每个测试文件重复搭建环境。

新增功能时应落入已有领域文件；当单文件同时出现两个独立变化原因，或超过约 400 行且能形成清晰职责边界时，再按上述方式拆分。
