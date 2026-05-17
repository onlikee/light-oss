<p align="center">
  <img src="./frontend/public/LOGO.svg" alt="Light OSS Logo" width="192" />
</p>

<h1 align="center">light-oss</h1>

轻量对象存储与静态站点托管 MVP，包含 Go 后端 API、React 管理台、MySQL 元数据，以及基于本地文件系统的对象内容存储。

它适合内部工具、原型系统、轻量私有部署和需要“可控、简单、能跑起来”的对象存储场景。项目当前不是完整的 S3 兼容服务，更偏向一套清晰、可二次开发的 OSS 基础实现。

## 项目介绍

Light OSS 由四部分组成：

- `backend/`：Go + Gin API，负责 Bucket、对象、目录浏览、签名下载、站点绑定等后端能力。
- `frontend/`：React + TypeScript + Vite 管理台，用于配置连接、管理 Bucket、浏览对象、发布站点。
- `gateway/`：Nginx 网关，按域名将请求转发到前端控制台或后端。
- `docker-compose.yml`：本地一键拉起 MySQL、后端、前端和网关。

项目内还提供了：

- OpenAPI 文档：`backend/docs/openapi.apifox.json`
- 数据库 migration：`backend/migrations/`
- Makefile 快捷命令：`make up`、`make test` 等

## 后端核心功能清单

- Bucket 管理：创建 Bucket、列出 Bucket、删除 Bucket（级联删除对象与关联站点）。
- 对象管理：上传、下载、HEAD 查看元数据、按 `prefix/cursor/limit` 列表查询、删除对象。
- 访问控制：支持 `public/private` 可见性，私有对象支持 Bearer Token 鉴权和签名下载 URL。
- 目录浏览：支持完整文件夹树、创建文件夹、删除文件夹、按目录浏览 entries、搜索与分页。
- 批量上传：支持 `multipart/form-data + manifest` 方式批量上传整个文件夹。
- 站点托管：将某个 `bucket + root_prefix` 绑定为静态站点，支持 `index document`、`error document`、`SPA fallback`。
- 运行保护：提供健康检查、上传体积限制、基础限流、`request_id` 与结构化错误响应。

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

## 运行指南

### 运行前准备

- Go 1.22+
- Node.js 20+
- npm
- MySQL 8.x
- Docker Desktop 或 Docker Engine（如果你打算用 Compose）

### 先看这几个关键事实

- 后端本地启动会优先加载根目录 `.env.personal`，不存在时再加载 `.env`；命中 `.env.personal` 后不会再回退 `.env` 补齐缺失项。
- 前端本地 `npm run dev` / `npm run build` 也会优先读取根目录 `.env.personal` 里的默认连接配置；如果浏览器 `localStorage` 里已经保存过设置，则仍以浏览器已保存值为准。
- 前端是 Vite 应用，本地开发默认地址是 `http://localhost:3000`。
- 如果你走 Docker Compose + 网关模式，对外唯一公开入口是 `gateway:80`。
- Compose 下 `backend` 和 `frontend` 默认不直接暴露宿主机端口；API 和控制台都应该通过网关域名访问。

### 模式差异：本地开发 vs Docker 网关

本地直跑优先读取根目录 `.env.personal`，不存在时再读取 `.env`；Docker Compose 仍按根目录 `.env` 工作。以下变量在两种模式下的推荐值不同：

| 变量                        | 本地开发建议                                                        | Docker 网关建议           | 说明                                     |
| --------------------------- | ------------------------------------------------------------------- | ------------------------- | ---------------------------------------- |
| `APP_PUBLIC_BASE_URL`       | `http://localhost:8080`                                             | `http://api.localhost`    | 影响签名下载链接生成。                   |
| `APP_STORAGE_ROOT`          | `./light-oss-data/storage` 或 Windows 下 `.\light-oss-data\storage` | `/data/storage`           | Compose 中后端卷挂载在 `/data/storage`。 |
| `VITE_DEFAULT_API_BASE_URL` | `http://localhost:8080`                                             | `http://api.localhost`    | 前端首次加载时默认使用的 API 地址。      |

### 本地运行

如果你想本机直接跑前后端，最省事的方式是只用 Docker 提供 MySQL，然后本地运行后端和前端。

#### 1. 准备根目录 `.env` 或 `.env.personal`

如果你需要保留一份不提交的本地专用配置，直接新建根目录 `.env.personal` 即可。本地直跑会优先读取 `.env.personal`，并完全替代 `.env`；缺失的键不会再从 `.env` 补齐，所以 `.env.personal` 应包含一份完整的本地配置。

直接编辑根目录 `.env` 或 `.env.personal`，至少确认下面这些值符合本地开发场景：

```env
APP_ENV=development
APP_ADDR=:8080
APP_PUBLIC_BASE_URL=http://localhost:8080
APP_STORAGE_ROOT=./light-oss-data/storage
APP_BEARER_TOKENS=light-oss
APP_SIGNING_SECRET=change-me-in-local-dev

DB_DSN=root:112233ss@tcp(localhost:3306)/light-oss?charset=utf8mb4&parseTime=True&loc=UTC&multiStatements=true

VITE_DEFAULT_API_BASE_URL=http://localhost:8080
VITE_DEFAULT_BEARER_TOKEN=light-oss
```

#### 2. 启动 MySQL

如果本机没有现成 MySQL，可以只拉起数据库服务：

```bash
docker compose up -d mysql
```

MySQL 默认会映射到宿主机 `3306`。

#### 3. 启动后端

```bash
cd backend
go test ./...
go run ./cmd/server
```

后端默认监听：

- `http://localhost:8080`

#### 4. 启动前端

另开一个终端：

```bash
cd frontend
npm install
npm test
npm run dev
```

前端默认地址：

- `http://localhost:3000`

#### 5. 打开管理台

- 前端：`http://localhost:3000`
- 后端 API：`http://localhost:8080`
- 设置页：进入 `/settings`，确认 `API Base URL` 和 `Bearer Token`

### Docker Compose 运行

如果你希望前端、后端、MySQL、网关一起跑起来，并且完整验证域名路由与站点托管，建议使用 Compose。

#### 1. 调整根目录 `.env`

Docker Compose 仍默认读取根目录 `.env`，不会自动采用 `.env.personal`。如果你要走网关域名模式，建议至少改成下面这些值：

```env
APP_PUBLIC_BASE_URL=http://api.localhost
APP_STORAGE_ROOT=/data/storage
VITE_DEFAULT_API_BASE_URL=http://api.localhost
VITE_DEFAULT_BEARER_TOKEN=light-oss
```

说明：

- `gateway` 是唯一对外入口，暴露端口 `80`
- `backend` 与 `frontend` 在 Compose 内部互通，但默认不直接暴露给宿主机
- `APP_STORAGE_ROOT` 在 Compose 下应指向容器内路径 `/data/storage`
- 自定义站点域名需要由你自行通过 DNS 或 hosts 指向 gateway

#### 2. 配置 hosts

本地验证域名路由时，通常需要把这些域名指向 `127.0.0.1`：

```text
127.0.0.1 console.localhost
127.0.0.1 api.localhost
127.0.0.1 demo.localhost
```

#### 3. 启动服务

```bash
docker compose up --build
```

或使用 Makefile：

```bash
make up
```

#### 4. 访问地址

- 控制台：`http://console.localhost`
- API：`http://api.localhost`
- MySQL：`localhost:3306`
- 站点示例：`http://demo.localhost`

#### 5. 网关拓扑说明

- `console.localhost` -> frontend
- `api.localhost` -> backend API
- 其他域名 -> backend 网站托管解析

需要特别注意：

- Nginx `gateway` 只保留原始 `Host` 并反向代理。
- 真正的 `Host -> site -> bucket + root_prefix` 解析发生在后端。
- 网关当前只处理 HTTP；HTTPS 证书、TLS 终止和正式 DNS 管理不在这个 MVP 内。

## `.env` / `.env.personal` 配置说明

根目录 `.env` 用于 Compose 启动；本地直跑时会优先读取根目录 `.env.personal`，不存在时再读取 `.env`。一旦命中 `.env.personal`，不会再从 `.env` 补齐缺失项。

前端设置页若已在浏览器 `localStorage` 保存过 `API Base URL` 或 `Bearer Token`，仍以浏览器已保存值为准；这里的 `VITE_*` 变量只影响首次加载时的默认值。

后端默认允许所有来源访问，无需额外配置 CORS 来源白名单。

> 不要把真实生产密码、签名密钥、域名配置直接提交到公开仓库。

### MySQL

| 变量                  | 示例值      | 说明                           |
| --------------------- | ----------- | ------------------------------ |
| `MYSQL_DATABASE`      | `light-oss` | MySQL 初始化时创建的数据库名。 |
| `MYSQL_USER`          | `root`      | 应用连接数据库时使用的用户名。 |
| `MYSQL_PASSWORD`      | `112233ss`  | `MYSQL_USER` 对应的密码。      |
| `MYSQL_ROOT_PASSWORD` | `112233ss`  | MySQL root 账户密码。          |

### 后端

| 变量                                 | 示例值                                                                                                    | 说明                                                       |
| ------------------------------------ | --------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `APP_ENV`                            | `development`                                                                                             | 后端运行环境，常见值为 `development` 或 `production`。     |
| `APP_ADDR`                           | `:8080`                                                                                                   | 后端 HTTP 服务监听地址。                                   |
| `APP_PUBLIC_BASE_URL`                | `http://localhost:8080`                                                                                   | 生成签名下载链接等场景使用的对外基础地址。                 |
| `APP_STORAGE_ROOT`                   | `./light-oss-data/storage`                                                                                | 对象内容存储根目录；Compose 模式建议改为 `/data/storage`。 |
| `APP_MAX_UPLOAD_SIZE_BYTES`          | `1073741824`                                                                                              | 单次上传请求允许的最大体积，单位字节。                     |
| `APP_MAX_MULTIPART_MEMORY_BYTES`     | `8388608`                                                                                                 | 处理 `multipart/form-data` 时允许驻留内存的大小。          |
| `APP_RATE_LIMIT_RPS`                 | `5`                                                                                                       | 每秒允许的请求速率。                                       |
| `APP_RATE_LIMIT_BURST`               | `10`                                                                                                      | 突发请求桶容量。                                           |
| `APP_BEARER_TOKENS`                  | `light-oss`                                                                                               | 后端允许的 Bearer Token 列表，多个值用逗号分隔。           |
| `APP_SIGNING_SECRET`                 | `change-me-in-local-dev`                                                                                  | 私有对象签名下载链接使用的密钥。                           |
| `APP_DEFAULT_SIGNED_URL_TTL_SECONDS` | `300`                                                                                                     | 默认签名下载链接有效期，单位秒。                           |
| `APP_MAX_SIGNED_URL_TTL_SECONDS`     | `86400`                                                                                                   | 签名下载链接允许设置的最大有效期。                         |
| `APP_READ_HEADER_TIMEOUT_SECONDS`    | `10`                                                                                                      | HTTP 请求头读取超时时间。                                  |
| `APP_SHUTDOWN_TIMEOUT_SECONDS`       | `10`                                                                                                      | 优雅关闭时允许等待的最长时间。                             |
| `DB_DSN`                             | `root:112233ss@tcp(localhost:3306)/light-oss?charset=utf8mb4&parseTime=True&loc=UTC&multiStatements=true` | 本地运行后端时使用的 MySQL DSN。                           |
| `DB_MAX_OPEN_CONNS`                  | `10`                                                                                                      | 数据库连接池最大打开连接数。                               |
| `DB_MAX_IDLE_CONNS`                  | `5`                                                                                                       | 数据库连接池最大空闲连接数。                               |
| `DB_CONN_MAX_LIFETIME_MINUTES`       | `30`                                                                                                      | 单个数据库连接的最长复用时间。                             |

### 前端

| 变量                        | 示例值                  | 说明                                    |
| --------------------------- | ----------------------- | --------------------------------------- |
| `VITE_DEFAULT_API_BASE_URL` | `http://localhost:8080` | 前端首次加载时默认填入的 API Base URL。 |
| `VITE_DEFAULT_BEARER_TOKEN` | `light-oss`             | 前端首次加载时默认填入的 Bearer Token。 |

## curl 示例

下面的示例默认按 Bash 写法展示。

如果你是“本地直接运行后端”，请使用：

```bash
BASE_URL=http://localhost:8080
```

如果你是“Docker Compose + gateway 域名方式”，请改成：

```bash
BASE_URL=http://api.localhost
```

其余示例通用：

```bash
TOKEN=light-oss
BUCKET=demo-bucket
PUBLIC_KEY=docs/hello.txt
PRIVATE_KEY=private/secret.txt
SITE_PREFIX=site-demo/
SITE_DOMAIN=demo.localhost
SITE_ID=1
```

常见响应格式说明：

- 成功响应通常为 `{"request_id":"...","data":...}`
- 失败响应通常为 `{"request_id":"...","error":{"code":"...","message":"..."}}`
- `204 No Content` 没有响应体

### 1. 无鉴权健康检查

```bash
curl "$BASE_URL/healthz"
```

### 2. 鉴权健康检查

```bash
curl "$BASE_URL/api/v1/healthz" \
  -H "Authorization: Bearer $TOKEN"
```

### 3. 创建 Bucket

Bucket 名称应使用小写字母、数字、点和短横线。

```bash
curl -X POST "$BASE_URL/api/v1/buckets" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"$BUCKET\"}"
```

### 4. 列出 Bucket

```bash
curl "$BASE_URL/api/v1/buckets" \
  -H "Authorization: Bearer $TOKEN"
```

### 删除 Bucket（危险操作）

这个接口会删除整个 Bucket，并级联删除：

- Bucket 下的所有对象与文件夹标记对象
- 绑定到该 Bucket 的站点配置与域名绑定

```bash
curl -X DELETE "$BASE_URL/api/v1/buckets/$BUCKET" \
  -H "Authorization: Bearer $TOKEN"
```

### 5. 上传 public 对象

```bash
curl -X PUT "$BASE_URL/api/v1/buckets/$BUCKET/objects/$PUBLIC_KEY" \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Object-Visibility: public" \
  -H "X-Original-Filename: hello.txt" \
  -H "Content-Type: text/plain" \
  --data-binary "hello world"
```

### 6. 上传 private 对象

```bash
curl -X PUT "$BASE_URL/api/v1/buckets/$BUCKET/objects/$PRIVATE_KEY" \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Object-Visibility: private" \
  -H "X-Original-Filename: secret.txt" \
  -H "Content-Type: text/plain" \
  --data-binary "top secret"
```

### 7. 下载 public 对象

public 对象可匿名下载：

```bash
curl "$BASE_URL/api/v1/buckets/$BUCKET/objects/$PUBLIC_KEY"
```

private 对象则需要 Bearer Token，或者先申请签名下载链接：

```bash
curl "$BASE_URL/api/v1/buckets/$BUCKET/objects/$PRIVATE_KEY" \
  -H "Authorization: Bearer $TOKEN"
```

### 8. 查看对象元数据（HEAD）

public 对象：

```bash
curl -I "$BASE_URL/api/v1/buckets/$BUCKET/objects/$PUBLIC_KEY"
```

private 对象需要 Bearer Token：

```bash
curl -I "$BASE_URL/api/v1/buckets/$BUCKET/objects/$PRIVATE_KEY" \
  -H "Authorization: Bearer $TOKEN"
```

### 9. 列出对象

```bash
curl "$BASE_URL/api/v1/buckets/$BUCKET/objects?prefix=docs/&limit=20&cursor=" \
  -H "Authorization: Bearer $TOKEN"
```

### 10. 删除对象

```bash
curl -X DELETE "$BASE_URL/api/v1/buckets/$BUCKET/objects/$PUBLIC_KEY" \
  -H "Authorization: Bearer $TOKEN"
```

### 11. 批量上传整个文件夹

这个接口使用 `multipart/form-data + manifest`。`manifest` 是一个 JSON 数组，每项都要声明：

- `file_field`
- `relative_path`

示例：

```bash
curl -X POST "$BASE_URL/api/v1/buckets/$BUCKET/objects/batch" \
  -H "Authorization: Bearer $TOKEN" \
  -F "prefix=$SITE_PREFIX" \
  -F "visibility=public" \
  -F 'manifest=[{"file_field":"file_0","relative_path":"index.html"},{"file_field":"file_1","relative_path":"assets/app.js"}]' \
  -F "file_0=@./dist/index.html;type=text/html" \
  -F "file_1=@./dist/assets/app.js;type=application/javascript"
```

上面的请求会把文件上传到：

- `site-demo/index.html`
- `site-demo/assets/app.js`

### 12. 列出完整文件夹树

```bash
curl "$BASE_URL/api/v1/buckets/$BUCKET/folders" \
  -H "Authorization: Bearer $TOKEN"
```

### 13. 创建文件夹

```bash
curl -X POST "$BASE_URL/api/v1/buckets/$BUCKET/folders" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"prefix":"docs/","name":"archive"}'
```

### 14. 递归删除文件夹

```bash
curl -X DELETE "$BASE_URL/api/v1/buckets/$BUCKET/folders?path=$SITE_PREFIX&recursive=true" \
  -H "Authorization: Bearer $TOKEN"
```

### 15. 按目录列出 entries

支持目录前缀、搜索和分页：

```bash
curl "$BASE_URL/api/v1/buckets/$BUCKET/entries?prefix=docs/&search=hello&limit=20&cursor=" \
  -H "Authorization: Bearer $TOKEN"
```

### 16. 下载文件夹 ZIP 压缩包

只支持非根目录文件夹，且需要 Bearer Token：

```bash
curl "$BASE_URL/api/v1/buckets/$BUCKET/folders/archive?path=docs/" \
  -H "Authorization: Bearer $TOKEN" \
  -o docs.zip
```

### 17. 修改对象可见性

```bash
curl -X PATCH "$BASE_URL/api/v1/buckets/$BUCKET/objects/visibility/$PRIVATE_KEY" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"visibility":"public"}'
```

### 18. 生成 private 对象签名下载 URL

```bash
curl -X POST "$BASE_URL/api/v1/sign/download" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"bucket\":\"$BUCKET\",\"object_key\":\"$PRIVATE_KEY\",\"expires_in_seconds\":300}"
```

返回结果中的 `data.url` 即可用于临时下载 private 对象。

### 19. 创建站点绑定

站点域名使用用户输入的完整域名，并且被访问的站点资源必须是 public 对象。本地默认示例可以使用 `demo.localhost`。

```bash
curl -X POST "$BASE_URL/api/v1/sites" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"bucket\":\"$BUCKET\",\"root_prefix\":\"$SITE_PREFIX\",\"enabled\":true,\"index_document\":\"index.html\",\"error_document\":\"404.html\",\"spa_fallback\":true,\"domains\":[\"$SITE_DOMAIN\"]}"
```

### 20. 列出站点

```bash
curl "$BASE_URL/api/v1/sites" \
  -H "Authorization: Bearer $TOKEN"
```

### 21. 查看单个站点

```bash
curl "$BASE_URL/api/v1/sites/$SITE_ID" \
  -H "Authorization: Bearer $TOKEN"
```

### 22. 更新站点

```bash
curl -X PUT "$BASE_URL/api/v1/sites/$SITE_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"bucket\":\"$BUCKET\",\"root_prefix\":\"$SITE_PREFIX\",\"enabled\":true,\"index_document\":\"index.html\",\"error_document\":\"404.html\",\"spa_fallback\":false,\"domains\":[\"$SITE_DOMAIN\"]}"
```

### 23. 删除站点

```bash
curl -X DELETE "$BASE_URL/api/v1/sites/$SITE_ID" \
  -H "Authorization: Bearer $TOKEN"
```

### 24. 通过站点 ID 访问站点内容

这个方式直接走后端，不依赖网关域名。

```bash
curl "$BASE_URL/sites/$SITE_ID"
```

### 25. 通过域名访问站点内容

如果你已经配置了 hosts，可直接访问：

```bash
curl "http://$SITE_DOMAIN/"
```

如果你在本地只想验证网关转发，也可以显式传 `Host`：

```bash
curl -H "Host: $SITE_DOMAIN" http://127.0.0.1/
```

## 前端使用说明

前端本地开发地址默认是 `http://localhost:3000`，Docker 网关方式建议通过 `http://console.localhost` 打开。

### `/settings`

- 配置 `API Base URL`
- 配置 `Bearer Token`
- 点击“测试连接”检查 API 可达性和后端健康状态
- 设置会保存在浏览器 `localStorage`

### `/dashboard`

- 查看当前连接的 API 主机
- 查看 Bearer Token 是否已配置
- 查看 Bucket 总数与最近更新时间等概览信息

### `/buckets`

- 创建新的 Bucket
- 查看已有 Bucket 列表
- 按 Bucket 名称搜索已有 Bucket
- 删除整个 Bucket（会级联删除其中的文件与关联站点）

### `/buckets/:bucket`

- 以目录方式浏览对象
- 搜索 entries
- 按页查看结果
- 上传单个文件
- 上传整个文件夹
- 创建文件夹
- 将某个文件夹直接下载为 ZIP 压缩包
- 删除文件或文件夹
- 切换对象 `public/private` 可见性
- 为 private 对象生成签名下载链接
- 从某个目录直接发布静态站点

### `/sites`

- 创建站点绑定
- 查看站点列表
- 编辑站点配置
- 删除站点

## 常见问题

### 1. 前端提示 401 或未授权怎么办？

先检查 `/settings` 页面里的 `Bearer Token` 是否与根目录 `.env` 中的 `APP_BEARER_TOKENS` 一致。后端是按 Bearer Token 白名单做鉴权的，值不匹配就会返回 401。

### 2. 为什么 private 对象不能直接打开？

这是预期行为。private 对象下载需要：

- 直接携带 `Authorization: Bearer ...`
- 或先调用 `/api/v1/sign/download` 生成临时签名链接

同时，private 对象的 `HEAD` 请求也需要 Bearer Token。

### 3. 为什么签名下载链接返回的地址不对？

通常是 `APP_PUBLIC_BASE_URL` 配错了。这个值会参与签名下载 URL 的生成：

- 本地直连后端：建议 `http://localhost:8080`
- 走 gateway 域名：建议 `http://api.localhost`

### 4. Docker / MySQL / 后端启动失败怎么办？

优先检查下面几项：

- Docker Desktop 是否已启动
- 根目录 `.env` 是否存在且格式正确
- `MYSQL_*` 变量是否可用
- 本机 `3306` 是否被占用
- Compose 模式下 `APP_STORAGE_ROOT` 是否仍然写成了本地路径，而不是 `/data/storage`

后端虽然会自动等待数据库并执行 migration，但如果数据库账户、密码或 DSN 本身错误，仍然会启动失败。

### 5. 为什么 Docker 启动后打不开 `console.localhost` 或 `api.localhost`？

通常是 hosts 没配，或者浏览器请求没有落到本机。请确认本地 hosts 中已经加入：

```text
127.0.0.1 console.localhost
127.0.0.1 api.localhost
```

同时确认 Compose 已经启动并且 `gateway` 成功监听 `80` 端口。

### 6. 为什么前端连不上后端 API？

常见原因有三类：

- `/settings` 中 `API Base URL` 填错了
- 你在 Docker 网关模式下仍然把 API 指向了 `http://localhost:8080`

建议：

- 本地直连开发：前端使用 `http://localhost:8080`
- Docker 网关模式：前端使用 `http://api.localhost`

### 7. 为什么站点域名不生效或返回 404？

请依次检查：

- 域名是否是合法 hostname，并且已在站点绑定中配置
- hosts 是否把该域名指向了 `127.0.0.1`
- 站点绑定里的 `bucket` 与 `root_prefix` 是否正确
- `index_document` 是否真实存在
- 被访问的站点文件是否都是 public 对象
- 是否误删了站点或把站点设成了禁用状态

### 8. 为什么通过域名访问不到站点资源，但用 `/sites/{siteID}` 可以访问？

一般说明后端站点配置没问题，但本地域名路由没打通。重点检查：

- 网关是否已启动
- hosts 是否生效
- 请求时是否把正确的 `Host` 头传到了网关

本地排查时可以先用：

```bash
curl -H "Host: demo.localhost" http://127.0.0.1/
```

## 已知限制

- 当前使用本地文件系统保存对象内容，不是分布式对象存储。
- 站点托管只对 public 对象生效。
- gateway 当前只处理 HTTP，不包含 HTTPS、证书签发或 TLS 终止。
- 项目目标是轻量 OSS MVP，不是完整 S3 兼容实现。

## 调试与测试

```bash
cd backend && go test ./...
cd frontend && npm test
docker compose logs -f mysql
docker compose logs -f backend
docker compose logs -f frontend
docker compose logs -f gateway
```
