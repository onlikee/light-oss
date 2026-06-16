<p align="center">
  <img src="./frontend/public/LOGO.svg" alt="Light OSS Logo" width="192" />
</p>

<h1 align="center">light-oss</h1>

<p align="center">
  A lightweight object storage and static site hosting MVP with a Go API, React console, MySQL metadata, and local filesystem object storage.
</p>

<p align="center">
  English | <a href="./README.zh-CN.md">Chinese</a>
</p>

## Overview

light-oss is designed for internal tools, prototypes, lightweight private deployments, and systems that need a simple object storage base that is easy to run and modify.

It is not a full S3-compatible service. The current implementation focuses on a clear MVP: bucket management, object upload/download, directory browsing, signed downloads, static site hosting, and a web console.

## Components

- `backend/`: Go + Gin API for buckets, objects, folders, signed URLs, and site bindings.
- `frontend/`: React + TypeScript + Vite management console.
- `gateway/`: Nginx gateway for console, API, and hosted-site routing.
- `docker-compose.yml`: local stack for MySQL, backend, frontend, and gateway.

## Features

- Bucket create/list/delete, including cascading object and site cleanup.
- Object upload, download, metadata lookup, listing, deletion, and `public` / `private` visibility.
- Bearer Token authentication for private APIs and private object access.
- Signed download URLs for private objects.
- Folder tree, folder creation/deletion, directory entries, search, pagination, and ZIP folder download.
- Batch folder upload with `multipart/form-data` and a manifest.
- Static site hosting from `bucket + root_prefix`, with custom domains, index document, error document, and SPA fallback.
- Health checks, upload size limits, basic rate limiting, `request_id`, and structured error responses.

## Tech Stack

- Backend: Go 1.22+, Gin, GORM, golang-migrate
- Frontend: React, TypeScript, Vite, TanStack Query, Axios
- UI: Tailwind CSS, Radix / shadcn-style components
- Database: MySQL 8.x
- Gateway: Nginx

## Project Layout

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

## Quick Start

### Prerequisites

- Go 1.22+
- Node.js 20+
- npm
- MySQL 8.x
- Docker Desktop or Docker Engine, if you use Compose

### Local Development

The simplest local setup is to run MySQL with Docker, then run the backend and frontend directly on your machine.

1. Prepare the root `.env` or `.env.personal`.

   Local backend and frontend commands prefer `.env.personal` when it exists. If `.env.personal` is found, `.env` is not merged for missing values, so keep `.env.personal` complete.

   Recommended local values:

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

2. Start MySQL.

   ```bash
   docker compose up -d mysql
   ```

3. Start the backend.

   ```bash
   cd backend
   go test ./...
   go run ./cmd/server
   ```

   The API listens on `http://localhost:8080` by default.

4. Start the frontend in another terminal.

   ```bash
   cd frontend
   npm install
   npm test
   npm run dev
   ```

   The console runs at `http://localhost:3000` by default.

5. Open `/settings` in the console and confirm the API Base URL and Bearer Token.

### Docker Compose with Gateway

Use this mode when you want to verify the full gateway and hosted-site domain flow.

1. Adjust the root `.env` for gateway mode.

   ```env
   APP_PUBLIC_BASE_URL=http://api.localhost
   APP_STORAGE_ROOT=/data/storage
   VITE_DEFAULT_API_BASE_URL=http://api.localhost
   VITE_DEFAULT_BEARER_TOKEN=light-oss
   ```

2. Add local hosts entries if needed.

   ```text
   127.0.0.1 console.localhost
   127.0.0.1 api.localhost
   127.0.0.1 demo.localhost
   ```

3. Start the stack.

   ```bash
   docker compose up --build
   ```

   Or:

   ```bash
   make up
   ```

4. Visit the services.

   - Console: `http://console.localhost`
   - API: `http://api.localhost`
   - MySQL: `localhost:3306`
   - Example hosted site: `http://demo.localhost`

Gateway routing:

- `console.localhost` -> frontend
- `api.localhost` -> backend API
- other hostnames -> backend static site resolver

Custom site domains must point to the gateway through DNS or hosts. The gateway currently handles HTTP only; HTTPS, certificate management, and production DNS automation are outside this MVP.

## Configuration Notes

- Root `.env` is used by Docker Compose.
- Root `.env.personal` is preferred by local backend/frontend commands when it exists.
- Frontend settings saved in browser `localStorage` override `VITE_DEFAULT_API_BASE_URL` and `VITE_DEFAULT_BEARER_TOKEN`.
- `APP_PUBLIC_BASE_URL` controls generated signed download URLs.
- `APP_STORAGE_ROOT` should be a local path for direct local runs and `/data/storage` in Compose.
- `APP_BEARER_TOKENS` is the Bearer Token allowlist. Multiple tokens are comma-separated.
- Do not commit real production passwords, signing secrets, tokens, or domain configuration.

## API and Documentation

- OpenAPI document: `backend/docs/openapi.apifox.json`
- Main authenticated API prefix: `/api/v1`
- Public health check: `GET /healthz`
- Authenticated health check: `GET /api/v1/healthz`
- Object API path keys are full object paths, so nested `/` segments act as directory-like prefixes.
- Static sites only serve `public` objects.
- Successful JSON responses usually use `{"request_id":"...","data":...}`.
- Failed JSON responses usually use `{"request_id":"...","error":{"code":"...","message":"..."}}`.

For manual API testing, set:

```bash
BASE_URL=http://localhost:8080
TOKEN=light-oss
```

Then call authenticated endpoints with:

```bash
curl "$BASE_URL/api/v1/buckets" \
  -H "Authorization: Bearer $TOKEN"
```

## Development

```bash
make test
make lint

cd backend && go test ./...
cd frontend && npm test
cd frontend && npm run lint
cd frontend && npm run build
```

Useful logs in Compose mode:

```bash
docker compose logs -f mysql
docker compose logs -f backend
docker compose logs -f frontend
docker compose logs -f gateway
```

## Known Limits

- Object content is stored on the local filesystem, not in distributed storage.
- Static site hosting only serves public objects.
- Gateway mode currently supports HTTP only.
- The project is a lightweight OSS MVP, not a full S3-compatible implementation.
