# Summary: Database Migration Tool (EMR → SIMRS)

> Dokumen ini untuk **presentasi ke interviewer**. Bukan duplikat dari `implementation.md` — ini narrative ringkas yang bisa kamu baca ulang 15 menit sebelum interview.

---

## Masalah

RS Annisa punya **2 juta record pasien** di database EMR (Electronic Medical Record) yang harus dipindahkan ke sistem baru **SIMRS** (Sistem Manajemen Rumah Sakit). Dua database ini punya **struktur yang berbeda**:

| Aspek          | EMR (Source)                                  | SIMRS (Target)                                   |
| -------------- | --------------------------------------------- | ------------------------------------------------ |
| Primary Key    | `id_pasien` INT auto-increment                | `pasien_uuid` UUID                               |
| Nama           | `nama_depan` + `nama_belakang` (2 kolom)      | `nama_lengkap` (1 kolom, gabungan)               |
| Alamat         | `alamat`, `kota`, `provinsi`, `kode_pos`      | `alamat_lengkap`, `kota`, `provinsi`, `kode_pos` |
| Kontak darurat | `nama_kontak_darurat`, `nomor_kontak_darurat` | `nama_kontak_darurat`, `telepon_kontak_darurat`  |

**Bukan copy-paste.** Ada transformasi data: merge field, rename field, generate UUID baru dari INT.

---

## Solusi

**Golang ETL pipeline** — satu binary, berjalan sebagai HTTP service, migrasi on-demand via REST API.

### Arsitektur (Satu Kalimat)

Server-side cursor baca 5000 rows per batch dari EMR → transform in-place (merge nama, generate UUID v5) → COPY ke temp table di SIMRS → UPSERT via `ON CONFLICT` → checkpoint di-COMMIT **bersama** data dalam satu transaksi.

### Kenapa Begini?

1. **Server-side cursor (bukan `SELECT *`)** — 2M rows tidak boleh masuk memory sekaligus. `DECLARE CURSOR` + `FETCH 5000` = memory usage konstan ~50-80MB.

2. **UUID v5 deterministic** — `UUID v5(namespace, id_pasien)` = pasien dengan `id_pasien=12345` **selalu** menghasilkan UUID yang sama. Re-run migration = idempotent, tidak bikin duplikat. Bisa di-reverse (dari UUID trace balik ke source ID).

3. **COPY + temp table + UPSERT** — `CopyFrom()` via pgx = bulk insert paling cepat di PostgreSQL. Temp table + `INSERT...ON CONFLICT DO UPDATE` = idempotent upsert tanpa violate constraint.

4. **Checkpoint atomic** — `UPDATE migration.migration_jobs SET last_processed_id = X` di-COMMIT **dalam transaksi yang sama** dengan data batch. Kalau crash, data dan checkpoint selalu sinkron. Resume = lanjut dari `last_processed_id`.

---

## 6 Fitur yang Membedakan Ini dari "Script Migrasi Biasa"

### 1. Graceful Shutdown

`SIGTERM` / `SIGINT` (docker stop, Ctrl+C) → **tidak langsung mati**. Proses:

- Selesaikan batch yang sedang jalan
- COMMIT checkpoint terakhir
- Set job status = `paused`
- Close semua connection
- Exit clean

**Tanpa ini**: kill di tengah batch = checkpoint tidak update = wasted re-processing saat resume.

### 2. Job Deduplication

User klik "Start Migration" 2x → **tidak jadi 2 proses parallel** yang race condition.

Implementasi: **partial UNIQUE index** di PostgreSQL:

```sql
CREATE UNIQUE INDEX ON migration_jobs (source_table, target_table)
    WHERE status IN ('pending', 'running');
```

INSERT kedua → constraint violation → app return **409 Conflict**. DB-level guarantee, bukan app-level check yang bisa race.

### 3. Backpressure

Kalau EMR masih serve production, migration yang FETCH nonstop bisa bikin DB lambat.

Solusi: `batch_delay_ms` — configurable pause antar batch.

- Development: `0ms` (full speed)
- Production off-peak: `100ms` (+40 detik total, acceptable)
- Shared production: `500ms` (aggressive throttle)

Bisa di-set per-job via API body.

### 4. Rollback / Undo

Data salah setelah migrasi? `POST /api/jobs/:job_id/rollback`:

- Re-generate UUID v5 dari range `first_processed_id` → `last_processed_id`
- DELETE per-batch dari target (avoid long lock)
- Update job status → `rolled_back`

**Kenapa bisa?** Karena UUID v5 deterministic — kita tahu exactly UUID mana yang dihasilkan oleh job ini, tanpa perlu simpan mapping table.

### 5. Mapping Table (Audit Trail)

Tabel `migration.emr_simrs_id_map` menyimpan relasi `id_pasien` (INT, EMR) ↔ `pasien_uuid` (UUID, SIMRS) secara persistent.

**Kenapa bukan rely UUID v5 saja?**

- UUID v5 bergantung pada **namespace tetap**. Kalau namespace berubah → relasi hilang, rollback impossible.
- Kalau ada migrasi dari **sumber kedua** (RS merger) → `id_pasien=12345` bisa milik orang berbeda → UUID v5 bentrok.
- Audit/compliance: "pasien UUID `abc-123` berasal dari EMR id berapa, dimigrasikan kapan?" → query langsung, tanpa compute.

Cost: ~50MB untuk 2M rows. Di-INSERT **dalam transaksi yang sama** dengan data + checkpoint = zero extra round-trip.

### 6. Unit & Integration Tests

- **Unit tests** (table-driven): `transformer.go` — happy path, NULL fields, truncation (nama > 100 char), empty nama, UUID determinism.
- **Integration tests** (`testcontainers-go`): spin up 2 PostgreSQL containers → seed 100 rows → run full migration → assert: count match, field values benar, checkpoint updated, rollback works, dedup returns 409.

**Kenapa ini penting?** Tanpa tests, klaim "idempotent", "truncation handled", "rollback works" cuma kata-kata. Tests = **proof yang bisa di-demo ke interviewer**.

---

## API Design

```
POST   /api/jobs                    → Create job (source_table, target_table, batch_size, batch_delay_ms)
POST   /api/jobs/:job_id/start      → Mulai/resume migrasi
POST   /api/jobs/:job_id/stop       → Graceful stop (finish batch, pause)
POST   /api/jobs/:job_id/rollback   → Undo migration (DELETE migrated data)
GET    /api/jobs                    → List all jobs
GET    /api/jobs/:job_id            → Detail + progress real-time
GET    /api/tables                  → List table yang sudah di-support
```

**Job-based** — setiap migrasi = satu resource dengan lifecycle (`pending` → `running` → `completed`/`failed`/`paused` → `rolled_back`).

---

## Edge Cases yang Sudah Di-handle

| Case                                                            | Handling                                                            |
| --------------------------------------------------------------- | ------------------------------------------------------------------- |
| `nama_depan` + `nama_belakang` > 100 char (target VARCHAR(100)) | Truncate ke 100, log warning                                        |
| `nama_depan` dan `nama_belakang` NULL                           | Default "Unknown"                                                   |
| Crash di tengah batch                                           | Checkpoint atomic — resume exact dari `last_processed_id`           |
| Re-run migration yang sudah selesai                             | Upsert (`ON CONFLICT DO UPDATE`) — idempotent                       |
| Double-click start migration                                    | Partial unique index → 409 Conflict                                 |
| Source DB lambat karena migration                               | `batch_delay_ms` backpressure                                       |
| Data salah setelah migrasi                                      | Rollback via UUID range DELETE                                      |
| `SIGTERM` / Docker stop                                         | Graceful shutdown, checkpoint saved                                 |
| `source_table` tidak dikenal                                    | Table registry → 422 Unprocessable Entity + daftar supported tables |

---

## Performance Estimate

| Metric                                                     | Angka            |
| ---------------------------------------------------------- | ---------------- |
| Total rows                                                 | 2.000.000        |
| Batch size                                                 | 5.000            |
| Total batches                                              | 400              |
| Per-batch (FETCH + transform + COPY + UPSERT + checkpoint) | ~200-400ms       |
| **Total time (no backpressure)**                           | **~2-3 menit**   |
| Total time (100ms delay)                                   | ~3-4 menit       |
| Memory usage                                               | ~50-80MB konstan |

---

## Tech Stack & Justifikasi

| Pilihan                 | Kenapa                                                                                     |
| ----------------------- | ------------------------------------------------------------------------------------------ |
| Go 1.22+                | Goroutine untuk parallel concern (HTTP server + migration). Static binary, low memory.     |
| pgx/v5                  | Satu-satunya Go driver yang support native COPY protocol (`CopyFrom()`).                   |
| UUID v5                 | Deterministic = idempotent + traceable + rollback-able. UUID v4 random = none of those.    |
| `net/http` stdlib       | 6 endpoint. Framework (Gin/Echo) = unjustified dependency.                                 |
| zerolog                 | Structured JSON logging. Zero allocation.                                                  |
| Docker Compose profiles | `--profile local` = spin up 2 PG containers. Tanpa profile = connect ke external DB (VPS). |

---

## Yang Belum Ada (Improvement Roadmap)

Sengaja **tidak** dimasukkan ke Phase 1 karena bukan blocking requirement:

1. **Health check** — `/healthz`, `/readyz` untuk Docker/K8s
2. **Dry-run mode** — jalankan tanpa write ke target
3. **Migrasi tabel `dokter`** — 10K rows, butuh DDL target dari tim produk
4. **Observability** — Prometheus metrics, Grafana dashboard

Semuanya **sudah ada design**, tinggal implement. Arsitektur tidak perlu refactor.

---

## Satu Hal untuk Diingat Saat Interview

> "Ini bukan script migrasi one-time yang dibuang setelah selesai. Ini production-grade tool dengan checkpoint/resume, graceful shutdown, deduplication, backpressure, rollback, mapping table, table registry, dan tested — tapi tetap sederhana. Satu binary Go, dua koneksi PostgreSQL, zero external dependency."

---

## Kalau Ditanya "Kalau Production, Apa yang Kurang?"

Jawab **berurutan** (paling penting dulu):

1. **Health check** — "App belum bisa bilang 'saya ready' ke orchestrator"
2. **Metrics** — "Belum ada Prometheus endpoint untuk monitoring dashboard"
3. **Dry-run mode** — "Belum bisa test tanpa affect target DB"

Tapi langsung counter: "Tests sudah ada (unit + integration), mapping table sudah persistent, rollback sudah works — jadi core functionality sudah proven."

**Jangan** jawab semuanya sekaligus. Jawab top 2-3, lalu bilang "ada roadmap lengkap di dokumentasi, saya bisa walk-through kalau Bapak/Ibu mau."
