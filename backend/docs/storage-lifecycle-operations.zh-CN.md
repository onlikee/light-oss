# Blob 台账、对账与迁移操作手册

## 部署边界

- `APP_STORAGE_MODE=local` 与 `APP_RATE_LIMIT_BACKEND=local` 都是默认值；任一配置仍为 `local` 时，生产部署必须保持后端 `replicas: 1`。
- `APP_STORAGE_MODE=shared-filesystem` 已覆盖同一 MySQL 与共享根目录下的双实例交叉读写及故障接管测试；MySQL 共享 limiter 也已通过双 Router 并发验收。横向扩容必须同时设置 `APP_RATE_LIMIT_BACKEND=mysql`，并在目标共享卷上复验。
- `storage_blobs`、`system_storage_quotas.used_bytes/reserved_bytes` 和 `storage_cleanup_jobs` 是配额与 Blob 生命周期的事实来源，不要在服务运行时人工重置计数。

当前迁移目录只包含 `000001_init` 基线，适用于全新空数据库。首次启动会创建全部运行时表、初始化配额与限流容量单例行，并在存储根目录上完成身份绑定和全量对账后写入 `reconciled_at`。不要将旧版本应用与该基线混用。

### 共享文件卷契约

启用共享模式时设置：

```env
APP_STORAGE_MODE=shared-filesystem
APP_STORAGE_ROOT=/data/storage
```

- `APP_STORAGE_ROOT` 必须在启动前挂载；共享模式不会自动创建缺失目录，以免挂载失败后误写节点本地磁盘。
- 所有实例必须看到完全相同的 `objects/` 与 `staging/` 命名空间，并具备读、写、创建目录、原子 rename 和删除权限。
- 卷必须提供 RWX、跨实例一致可见，以及同一文件系统内原子 rename；`staging/` 与 `objects/` 不得跨文件系统或通过异步同步工具拼接。
- 根目录 `.storage-id` 是卷的持久身份哨兵，并与 MySQL `system_storage_quotas.storage_id` 绑定；不要删除、复制或手工修改。连接同一数据库但误挂另一空目录的实例会在启动时失败。
- readiness 会在当前实例执行“写入并同步 -> rename -> 读取校验 -> 删除”探针，但不能单独证明跨节点一致性。上线前必须从实例 A 上传、实例 B 读取和覆盖、再由实例 A 读取，并验证 cleanup lease 到期后可由另一实例接管。
- Docker 本地 named volume、节点本地 `hostPath` 或最终一致的文件同步目录不构成多实例共享卷；默认 Compose 因此继续固定 `replicas: 1`。

### 共享限流契约

启用多实例共享限流前，先确保 `000001_init` 已成功应用，然后让所有副本使用同一 MySQL 与完全一致的限流预算：

```env
APP_RATE_LIMIT_BACKEND=mysql
APP_RATE_LIMIT_CACHE_TTL_SECONDS=600
```

- `local` 后端使用进程内 TTL + LRU 有界缓存；`mysql` 后端通过 `rate_limit_capacity` 单例行在事务内维护全局条目数。两种模式都受 `APP_RATE_LIMIT_CACHE_MAX_ENTRIES` 硬上限约束，所有副本必须使用相同配置。
- `mysql` 后端按 `global-ip`、`public`、`management`、`upload`、`sign`、`health` 六个稳定命名空间保存 token bucket，并对组合 key 做 SHA-256，不把客户端 IP 或身份 scope 原文写入数据库。
- 每次令牌判定使用数据库 UTC 时钟和单行原子 upsert；不同实例争用同一行，因此新增副本不会新增 burst。数据库写入、读取或提交结果失败时请求返回 `503 / rate_limit_unavailable`，不会 fail open。
- `APP_RATE_LIMIT_CACHE_TTL_SECONDS` 是共享桶的空闲过期时间；进程会分批清理到期行并原子释放容量。容量已满且没有过期行时，新 key 返回 429，不创建无界状态，也不驱逐已有活跃桶。可用以下只读 SQL 检查规模和过期积压：

```sql
SELECT COUNT(*) AS rate_limit_buckets FROM rate_limit_buckets;
SELECT COUNT(*) AS expired_rate_limit_buckets
FROM rate_limit_buckets
WHERE expires_at <= UTC_TIMESTAMP(6);
SELECT entry_count, expired_evictions, capacity_rejections
FROM rate_limit_capacity
WHERE id = 1;
```

- `GET /api/v1/system/metrics` 的每类 limiter 指标包含 `backend`、`entries`、`max_entries`、`expired_evictions`、`capacity_rejections`、`rejected_requests` 和 `store_errors`；MySQL 模式从共享容量行读取全局计数。

## 启动和对账

应用启动会先执行带 MySQL advisory lock 的 migration，并创建或读取根目录 `.storage-id`，再与数据库中的持久身份做只写一次的原子绑定。`reconciled_at` 为空的首次新版本启动会在独立的存储对账 advisory lock 保护下扫描当前受管的 `objects/` 与 `staging/` 命名空间；readiness 探针文件会被显式排除。已存在完成标记时，常规启动先校验当前挂载卷身份，再跳过全量文件扫描，避免新副本启动期间把其他副本同时置为 unready。显式对账仍会清空 marker，并由该锁覆盖物理扫描和最终校验：

从仍写入 `tmp/` 的旧版本升级时，应先停止旧实例，人工检查并备份或清理该目录。新版本不再把 `tmp/` 视为受管命名空间，数据库迁移也不会修改物理存储。

- 未被台账引用的受管文件登记为 `orphaned` 并计入 `used_bytes`；
- active 台账缺少物理文件时启动失败；
- orphan 只登记和告警，不自动删除；
- 对账成功后写入 `system_storage_quotas.reconciled_at`，readiness 才会通过。

只执行 migration 与强制全量对账、不启动 HTTP 服务。该命令运行期间共享 marker 会暂时为空，因此先摘流量并停止所有服务实例：

```powershell
cd backend
go run ./cmd/server -reconcile-storage-only
```

查看清理积压和 staging 状态：

```sql
SELECT COUNT(*) AS cleanup_backlog FROM storage_cleanup_jobs;
SELECT id, storage_path, size, staging_lease_expires_at, created_at
FROM storage_blobs
WHERE status = 'staging'
ORDER BY staging_lease_expires_at, created_at;
SELECT id, storage_path, size, created_at
FROM storage_blobs
WHERE status = 'orphaned'
ORDER BY created_at;
```

不要直接删除 orphan 文件或台账行。确认其不再需要后，停止后端写流量，并按 Blob ID 将单个 orphan 原子转为 `pending_delete`、写入持久化清理任务：

```powershell
cd backend
go run ./cmd/server -enqueue-orphan-cleanup-id '<storage_blobs.id>'
```

该命令只接受 `orphaned`（或已经进入 `pending_delete` 的同一任务）且引用数为 0 的 Blob，入队后退出，不直接删除文件。随后正常启动后端，由具备 lease、退避和幂等删除语义的 cleanup worker 执行物理回收；可通过 metrics 或 `storage_cleanup_jobs` 确认完成情况。

## 回收站删除组

- 每次删除一个顶层文件或目录都会生成独立 `delete_group_id`；目录 marker 与该次删除包含的后代共享同一组，单次批量选择中的多个顶层项仍保持不同组。
- 回收站列表、目录恢复和永久删除都以 `delete_group_id` 为精确边界。即使两个重叠前缀在同一时间删除，也不会把另一操作的条目误恢复或误删。
- 不要手工复用、清空或改写 `delete_group_id`。排障时先按组查询：

```sql
SELECT delete_group_id, bucket_name, object_key, deleted_at
FROM recycle_bin_objects
WHERE delete_group_id = '<group-id>'
ORDER BY id;
```

## 故障恢复

- 请求失败时会同步补偿 staging；补偿失败会写入持久化 cleanup job。
- staging 创建时即在同一数据库事务内写入初始 lease；单上传使用一个 heartbeat，批量上传共享一个 heartbeat，并持续覆盖 Stage 到 Publish 的完整窗口。续租和过期判断都使用数据库 UTC 时钟。
- cleanup 与对账只会接管 lease 已到期的 staging；正在慢速上传的另一实例即使运行时间超过 TTL，也会因 heartbeat 续租而受到保护。进程中断后 heartbeat 停止，staging 才会在 `APP_STORAGE_STAGING_TTL_SECONDS` 到期后进入清理队列。
- worker 以数据库时间为 lease 时钟抢占任务；lease 到期后其他实例可以接管，节点墙钟偏差不会提前窃取或延后接管。
- 每次抢占使用唯一 fencing token；物理删除超过一个 lease 周期时会定期续租，旧 worker 不能完成已被新 owner 接管的任务。
- 物理删除幂等；只有物理删除成功后，worker 才扣减 `used_bytes` 或 `reserved_bytes` 并删除台账。
- 如果 MySQL `COMMIT` 响应丢失，应用使用带行锁的当前读等待原事务定局。只有全部 Blob 仍是 `staging` 时才会在同一确认事务中封存并入队；超时、缺行、混合状态或已激活状态都保留物理文件，不做盲目补偿。
- `DB_CONNECT_TIMEOUT_SECONDS` / `DB_READ_TIMEOUT_SECONDS` / `DB_WRITE_TIMEOUT_SECONDS` 为连接、读响应和写请求设置有界网络等待；生产环境调整时要同时考虑大表 migration 的最长执行时间。
- `GET /api/v1/system/metrics` 可查看 cleanup backlog、失败数、reservation 失败和连接池等待。

## Migration 验证

对临时 MySQL 8.x 实例执行 `up -> down -> up`，不要在包含业务数据的数据库上运行 down 测试：

```powershell
cd backend
$env:MYSQL_TEST_DSN = 'root:test-password@tcp(127.0.0.1:3306)/mysql?charset=utf8mb4&parseTime=true&loc=UTC&multiStatements=true'
go test ./integration -run '^TestMySQLMigrationsUpDownUp$' -count=1
go test ./integration -run '^TestMySQLConcurrentMigrationStartupUsesLock$' -count=1
go test ./integration -run '^TestMySQLAtomicQuotaReservationAcrossInstances$' -count=1
go test ./integration -run '^TestMySQLSharedRateLimitDoesNotScaleWithRouterCount$' -count=1
go test ./integration -run '^TestMySQLSharedRateLimitHasGlobalCapacityAndExpiry$' -count=1
go test ./integration -run '^TestMySQLSharedFilesystemCrossInstanceLifecycleAndTakeover$' -count=1
go test ./integration -run '^TestMySQLStorageIdentityRejectsDifferentSharedRoot$' -count=1
go test ./integration -run '^TestMySQLStagingHeartbeatPreventsCrossInstanceCleanup$' -count=1
```

测试会创建带 `light_oss_test_` 前缀的隔离数据库，并在结束时删除；DSN 用户必须具有创建和删除测试数据库的权限。

## Dirty migration

服务检测到 dirty migration 时会输出失败版本并立即退出，不会调用 `Force`。恢复步骤：

1. 停止所有后端实例并备份数据库与 Blob 根目录。
2. 检查 `schema_migrations` 的版本和 dirty 标记，对照对应的 `migrations/*.up.sql` 确认已执行到哪一条语句。
3. 人工完成或回滚失败 DDL，并核对对象、回收站、Blob 台账和配额计数。
4. 只有确认 schema 与目标版本完全一致后，才人工修复 migration 状态并重新启动一个实例。
5. readiness、对账和清理积压均正常后再恢复流量。

## 回滚到旧写路径前

回滚 SQL 只能人工执行。旧版本不理解 Blob reservation，回滚二进制前必须确认：

```sql
SELECT COUNT(*) FROM storage_blobs WHERE status = 'staging';
SELECT COUNT(*) FROM storage_cleanup_jobs;
SELECT reserved_bytes FROM system_storage_quotas WHERE id = 1;
```

三项必须均为 `0`，并应先备份数据库与物理存储。否则继续运行新版本完成恢复，不能直接降级。
