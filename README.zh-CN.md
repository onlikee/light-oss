<p align="center">
  <img src="./frontend/public/LOGO.svg" alt="Light OSS Logo" width="192" />
</p>

<h1 align="center">light-oss</h1>

<p align="center">
  轻量对象存储与静态站点托管 MVP，包含 Go 后端 API、React 管理台、MySQL 元数据，以及基于本地文件系统的对象内容存储。
</p>

<p align="center">
  <a href="./README.md">English</a> | 简体中文
</p>

## 项目概览

light-oss 适合内部工具、原型系统、轻量私有部署，以及需要一套简单、可运行、方便二次开发的对象存储基础实现的场景。

它不是完整的 S3 兼容服务。当前重点是清晰的 MVP：Bucket 管理、对象上传下载、目录浏览、签名下载、静态站点托管和 Web 管理台。

## 组成部分

- `backend/`：Go + Gin API，负责 Bucket、对象、目录、签名链接和站点绑定。
- `frontend/`：React + TypeScript + Vite 管理台。
- `gateway/`：Nginx 网关，负责控制台、API 和托管站点域名路由。
- `docker-compose.yml`：本地拉起 MySQL、后端、前端和网关。

## 核心功能

- Bucket 创建、列表、删除，并级联清理对象和站点配置。
- 对象上传、下载、元数据查询、列表、删除，以及 `public` / `private` 可见性。
- Bearer Token 鉴权，用于私有 API 和私有对象访问。
- 为 private 对象生成签名下载 URL。
- 文件夹树、创建/删除文件夹、目录 entries、搜索、分页和文件夹 ZIP 下载。
- 使用 `multipart/form-data` 和 manifest 批量上传文件夹。
- 从 `bucket + root_prefix` 发布静态站点，支持自定义域名、首页文档、错误页文档和 SPA fallback。
- 健康检查、上传体积限制、基础限流、`request_id` 和结构化错误响应。

## 技术栈

- 后端：Go 1.22+、Gin、GORM、golang-migrate
- 前端：React、TypeScript、Vite、TanStack Query、Axios
- UI：Tailwind CSS、Radix / shadcn 风格组件
- 数据库：MySQL 8.x
- 网关：Nginx

## 项目结构

```text
.
├─ backend/
│  ├─ cmd/server
│  ├─ docs/openapi.apifox.json
│  ├─ internal/
│  └─ migrations/
├─ frontend/
├─ gateway/
├─ docker-compose.yml
├─ Makefile
└─ .env
```

## 快速开始

### 环境要求

- Go 1.22+
- Node.js 20+
- npm
- MySQL 8.x
- Docker Desktop 或 Docker Engine，如果使用 Compose

### 本地开发

最简单的本地方式是用 Docker 跑 MySQL，然后在本机直接运行后端和前端。

1. 准备根目录 `.env` 或 `.env.personal`。

   本地后端和前端命令会优先读取 `.env.personal`。如果 `.env.personal` 存在，就不会再从 `.env` 补齐缺失值，所以 `.env.personal` 应保持完整。

   推荐本地配置：

   ```env
   APP_ENV=development
   APP_ADDR=:8080
   APP_STORAGE_MODE=local
   APP_RATE_LIMIT_BACKEND=local
   APP_STORAGE_ROOT=./light-oss-data/storage
   APP_BEARER_TOKENS=light-oss
   APP_SIGNING_SECRET=change-me-in-local-dev

   DB_DSN=root:112233ss@tcp(localhost:3306)/light-oss?charset=utf8mb4&parseTime=True&loc=UTC&multiStatements=true

   VITE_DEFAULT_API_BASE_URL=http://localhost:8080
   VITE_DEFAULT_BEARER_TOKEN=light-oss
   ```

2. 启动 MySQL。

   ```bash
   docker compose up -d mysql
   ```

3. 启动后端。

   ```bash
   cd backend
   go test ./...
   go run ./cmd/server
   ```

   后端默认监听 `http://localhost:8080`。

4. 另开终端启动前端。

   ```bash
   cd frontend
   npm install
   npm test
   npm run dev
   ```

   前端默认地址是 `http://localhost:3000`。

5. 打开管理台 `/settings`，确认 API Base URL 和 Bearer Token。

### Docker Compose + 网关

如果需要完整验证网关、域名路由和静态站点托管流程，使用这个模式。

1. 调整根目录 `.env`。

   ```env
   APP_STORAGE_MODE=local
   APP_RATE_LIMIT_BACKEND=local
   APP_STORAGE_ROOT=/data/storage
   VITE_DEFAULT_API_BASE_URL=http://api.localhost
   VITE_DEFAULT_BEARER_TOKEN=light-oss
   ```

2. 如有需要，添加本地 hosts。

   ```text
   127.0.0.1 console.localhost
   127.0.0.1 api.localhost
   127.0.0.1 demo.localhost
   ```

3. 启动服务。

   ```bash
   docker compose up --build
   ```

   或使用：

   ```bash
   make up
   ```

4. 访问服务。

   - 控制台：`http://console.localhost`
   - API：`http://api.localhost`
   - MySQL：`localhost:3306`
   - 托管站点示例：`http://demo.localhost`

网关路由：

- `console.localhost` -> frontend
- `api.localhost` -> backend API
- 其他 hostname -> backend 静态站点解析

自定义站点域名需要通过 DNS 或 hosts 指向 gateway。当前 gateway 只处理 HTTP；HTTPS、证书管理和正式 DNS 自动化不在这个 MVP 范围内。

## 配置说明

- 根目录 `.env` 用于 Docker Compose；容器内监听地址和存储根目录由 Compose 固定为 `:8080` 与 `/data/storage`，避免误用主机路径。
- 本地后端/前端命令会优先读取根目录 `.env.personal`。
- 浏览器 `localStorage` 中保存的前端设置会覆盖 `VITE_DEFAULT_API_BASE_URL` 和 `VITE_DEFAULT_BEARER_TOKEN`。
- 签名下载接口返回相对 API 路径，前端使用当前 API Base URL 补全下载地址。
- `APP_STORAGE_MODE` 默认为 `local`，此时服务会按需创建 `APP_STORAGE_ROOT`；本地直跑时应使用本机路径，默认 Compose 使用 `/data/storage`。
- `APP_STORAGE_MODE=shared-filesystem` 用于多实例共享文件卷。`APP_STORAGE_ROOT` 必须在启动前完成挂载，服务不会自动创建缺失目录；该卷必须提供 RWX、跨实例一致可见、同一文件系统内原子 rename，以及读/写/删除权限。根目录 `.storage-id` 会与数据库绑定，用来拒绝误挂其他卷的实例。
- 对象配额以 MySQL 中的 `used_bytes` / `reserved_bytes` 台账为准；`APP_CHUNK_SIZE_BYTES` 控制流式预留块大小，`APP_STORAGE_STAGING_TTL_SECONDS` 控制崩溃后 staging 的回收期限。
- `DB_CONNECT_TIMEOUT_SECONDS` / `DB_READ_TIMEOUT_SECONDS` / `DB_WRITE_TIMEOUT_SECONDS` 为 MySQL 网络等待设置有界超时（默认 5/300/30 秒），包括为事务提交响应不确定窗口设置上限。
- `APP_BEARER_TOKENS` 是 Bearer Token 白名单，多个 token 用逗号分隔。
- `APP_RATE_LIMIT_BACKEND` 默认为 `local`。设置为 `mysql` 后，鉴权前 IP 桶和各类鉴权后路由桶都通过共享的 `rate_limit_buckets` 表协调；所有副本必须使用一致的速率与 burst 配置。数据库判定失败时会 fail closed 并返回 HTTP 503，不会绕过限流。
- 限流分为两级：鉴权前由 `APP_RATE_LIMIT_IP_RPS` / `APP_RATE_LIMIT_IP_BURST` 按客户端 IP 粗限流，鉴权后的管理 API 使用 `APP_RATE_LIMIT_MANAGEMENT_RPS` / `APP_RATE_LIMIT_MANAGEMENT_BURST` 身份预算。
- 公开对象/站点下载、上传、签名链接创建和健康检查使用独立预算，可分别通过 `APP_RATE_LIMIT_PUBLIC_*`、`APP_RATE_LIMIT_UPLOAD_*`、`APP_RATE_LIMIT_SIGN_*`、`APP_RATE_LIMIT_HEALTH_*` 覆盖；每类路由都有独立的显式默认值。
- `APP_RATE_LIMIT_CACHE_TTL_SECONDS` 同时控制本地缓存淘汰与共享 MySQL 桶的过期时间；`APP_RATE_LIMIT_CACHE_MAX_ENTRIES` 对本地缓存和 MySQL 共享桶都设置硬上限。共享容量耗尽时，新 key 返回 429，已有 key 仍按原桶判定；过期行释放容量。`APP_TRUSTED_PROXIES` 是以逗号分隔的网关精确 IP 或 CIDR 白名单；留空时忽略转发客户端 IP 请求头，禁止配置全地址范围。
- `shared-filesystem` 已覆盖同一 MySQL 与共享卷上的双实例交叉上传、读取、覆盖、清理及 lease/staging 故障接管测试；MySQL limiter 也已通过双 Router 并发测试，证明只共享一个 burst。`APP_STORAGE_MODE=local` 或 `APP_RATE_LIMIT_BACKEND=local` 时，后端必须保持 `replicas: 1`；横向扩容必须同时启用两种共享模式，并按运维手册复验实际共享卷。
- 不要把真实生产密码、签名密钥、token 或域名配置提交到公开仓库。

## API 与文档

- OpenAPI 文档：`backend/docs/openapi.apifox.json`
- 上传性能烟测：`backend/docs/upload-performance-baseline.zh-CN.md`
- Blob 台账、对账与迁移操作：`backend/docs/storage-lifecycle-operations.zh-CN.md`
- 历史 schema 清理说明：`backend/docs/legacy-upload-session-tables.zh-CN.md`
- 后端代码组织约定：`backend/docs/code-organization.zh-CN.md`
- 主要鉴权 API 前缀：`/api/v1`
- 存活检查：`GET /livez`；就绪检查：`GET /readyz`
- 鉴权健康检查：`GET /api/v1/healthz`
- 鉴权运行时指标：`GET /api/v1/system/metrics`
- 对象 API 路径里的 key 是完整对象路径，嵌套 `/` 会表现为类似目录的前缀。
- 静态站点只会服务 public 对象。
- 成功 JSON 响应通常为 `{"request_id":"...","data":...}`。
- 失败 JSON 响应通常为 `{"request_id":"...","error":{"code":"...","message":"..."}}`。

升级提示：旧的公开 `GET /healthz` 别名以及 `APP_RATE_LIMIT_RPS` / `APP_RATE_LIMIT_BURST` 变量已移除，请改用 `/livez`、`/readyz` 和 `APP_RATE_LIMIT_MANAGEMENT_RPS` / `APP_RATE_LIMIT_MANAGEMENT_BURST`。迁移 `000012_remove_legacy_schema` 会删除未使用的上传会话表和 `objects.is_deleted`；升级前如需保留历史会话数据请先备份，且新旧后端版本不可混跑。

手动测试 API 时可以先设置：

```bash
BASE_URL=http://localhost:8080
TOKEN=light-oss
```

然后用 Bearer Token 调用鉴权接口：

```bash
curl "$BASE_URL/api/v1/buckets" \
  -H "Authorization: Bearer $TOKEN"
```

## 开发命令

```bash
make test
make lint

cd backend && go test ./...
cd backend && go test -race ./...
cd backend && go vet ./...
cd backend && gofmt -l .
cd frontend && npm test
cd frontend && npm run lint
cd frontend && npm run build
```

只执行受管 Blob 对账并退出（会登记并报告 orphan，不会自动删除）：

```bash
cd backend && go run ./cmd/server -reconcile-storage-only
```

Compose 模式下常用日志：

```bash
docker compose logs -f mysql
docker compose logs -f backend
docker compose logs -f frontend
docker compose logs -f gateway
```

## 已知限制

- 对象内容存储在本地文件系统，不是分布式对象存储。
- 静态站点托管只服务 public 对象。
- gateway 当前只支持 HTTP。
- 项目目标是轻量 OSS MVP，不是完整 S3 兼容实现。
