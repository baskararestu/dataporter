# DataPorter

REST API service for migrating patient records from an EMR PostgreSQL database to a SIMRS PostgreSQL database with full schema transformation.

## Features

- **ETL pipeline** — server-side cursor with `REPEATABLE READ`, batch transform, `COPY` + temp table + upsert
- **Schema transformation** — INT primary key → UUID v5 (deterministic), field renames, name merges
- **Resumable jobs** — checkpoint-based: pause, resume, or rollback any migration job
- **REST API** — 10 endpoints for full job lifecycle management
- **Swagger UI** — interactive API docs at `/swagger/index.html`
- **Structured logging** — zerolog JSON output with configurable level
- **12-factor config** — all settings via environment variables, no hardcoded values

---

## Architecture

```
main.go                 ← thin orchestrator (signal handling, wiring)
├── config/             ← ENV config loader (caarlos0/env)
├── logger/             ← zerolog global setup
├── database/           ← open/close source (pgx) + target (pgxpool) connections
├── server/             ← HTTP server lifecycle + graceful shutdown
├── api/
│   ├── handler.go      ← 10 HTTP handlers
│   └── router.go       ← Go 1.22 stdlib routing + /swagger/*
├── migration/
│   ├── migrator.go     ← ETL loop + rollback
│   ├── extractor.go    ← server-side cursor FETCH
│   ├── transformer.go  ← field mapping, UUID v5, name normalization
│   ├── loader.go       ← COPY + temp table + upsert
│   ├── registry.go     ← supported source→target table registry
│   └── validator.go    ← post-migration row count check
├── repository/
│   ├── job_repo.go     ← migration_jobs CRUD
│   └── mapping_repo.go ← emr_simrs_id_map CRUD
├── monitoring/         ← in-memory live progress tracker
├── model/              ← shared structs (MigrationJob, etc.)
└── docs/               ← generated Swagger docs (swag init)
```

---

## Requirements

- Go 1.22+
- PostgreSQL 14+
- [swag CLI](https://github.com/swaggo/swag) (only needed to regenerate docs)

---

## Quick Start

### 1. Clone and configure

```bash
git clone https://github.com/baskararestu/dataporter
cd dataporter
cp .env.example .env
# Edit .env with your DB credentials
```

### 2. Run locally (own PostgreSQL)

```bash
go run main.go
```

The server starts on `http://localhost:8080`.

### 3. Run with Docker (includes local PostgreSQL)

> **Note:** requires Docker Compose v2 (`docker compose` not `docker-compose`).

```bash
make up        # starts postgres_emr, postgres_simrs, and the app
make logs      # tail app logs
make down      # stop everything
```

---

## Environment Variables

All variables are **required** unless a default is noted.

### EMR Database (source — read-only)

| Variable | Default | Description |
|---|---|---|
| `EMR_DB_HOST` | — | PostgreSQL host |
| `EMR_DB_PORT` | — | PostgreSQL port |
| `EMR_DB_USER` | — | Database user |
| `EMR_DB_PASSWORD` | — | Database password |
| `EMR_DB_NAME` | — | Database name |
| `EMR_DB_SSL_MODE` | `disable` | SSL mode |

### SIMRS Database (target — read-write)

| Variable | Default | Description |
|---|---|---|
| `SIMRS_DB_HOST` | — | PostgreSQL host |
| `SIMRS_DB_PORT` | — | PostgreSQL port |
| `SIMRS_DB_USER` | — | Database user |
| `SIMRS_DB_PASSWORD` | — | Database password |
| `SIMRS_DB_NAME` | — | Database name |
| `SIMRS_DB_SSL_MODE` | `disable` | SSL mode |

### Application

| Variable | Default | Description |
|---|---|---|
| `BATCH_SIZE` | `5000` | Rows fetched per cursor FETCH |
| `BATCH_DELAY_MS` | `0` | Delay (ms) between batches (backpressure) |
| `HTTP_PORT` | `8080` | API server port |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |

---

## API Reference

Swagger UI: **`http://localhost:8080/swagger/index.html`**

### Jobs

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/jobs` | Create a new migration job (pending state) |
| `POST` | `/api/jobs/{job_id}/start` | Start or resume a job |
| `POST` | `/api/jobs/{job_id}/stop` | Pause a running job |
| `POST` | `/api/jobs/{job_id}/rollback` | Delete migrated rows and mark as rolled back |
| `GET` | `/api/jobs` | List all jobs (optional `?status=` filter) |
| `GET` | `/api/jobs/{job_id}` | Get job details + live progress counters |
| `GET` | `/api/jobs/{job_id}/validate` | Compare source vs target row counts |
| `GET` | `/api/jobs/{job_id}/errors` | Get full error log for a job |

### Metadata

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/tables` | List supported source→target table pairs |
| `GET` | `/healthz` | Health check |

### Example: run a full migration

```bash
# 1. Create job
curl -s -X POST http://localhost:8080/api/jobs \
  -H "Content-Type: application/json" \
  -d '{"source_table":"pasien","target_table":"pasien","batch_size":5000}'

# 2. Start job (replace <job_id> with the UUID from the response)
curl -s -X POST http://localhost:8080/api/jobs/<job_id>/start

# 3. Poll progress
curl -s http://localhost:8080/api/jobs/<job_id>

# 4. Validate after completion
curl -s http://localhost:8080/api/jobs/<job_id>/validate
```

---

## Schema Transformation (EMR → SIMRS)

| EMR field | SIMRS field | Transformation |
|---|---|---|
| `id` (INT) | `pasien_id` (UUID) | UUID v5 deterministic from source ID |
| `nama_depan` + `nama_belakang` | `nama_lengkap` | Concatenated, max 100 chars |
| `tgl_lahir` | `tanggal_lahir` | Direct copy |
| `jenis_kelamin` | `jenis_kelamin` | Direct copy |
| `alamat` | `alamat` | Truncated to 255 chars |
| `no_telepon` | `nomor_telepon` | Direct copy |
| `email` | `email` | Truncated to 100 chars |
| `created_at` | `created_at` | Direct copy |

---

## Development

```bash
make build      # compile binary → ./dataporter
make test       # run all tests with race detector
make coverage   # run tests + open HTML coverage report
make lint       # golangci-lint
make tidy       # go mod tidy
make swag       # regenerate docs/ from annotations
```

### Regenerating Swagger docs

After modifying handler annotations or adding new endpoints:

```bash
# Install swag CLI (one-time)
go install github.com/swaggo/swag/cmd/swag@v1.16.4

make swag
```

---

## License

MIT
