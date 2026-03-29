# Implementation Plan v2: EMR → SIMRS Data Migration Backend

> **Scope**: Phase 1 — Migrasi tabel `pasien` saja (2 juta record). Tabel `dokter` (10K record) di-defer ke Phase 2.

---

## 0. Koreksi dari v1

v1 punya blind spots yang harus ditambal sebelum nulis satu baris code:

| #   | Masalah di v1                                                                                                       | Fix                                                   |
| --- | ------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| 1   | **Tidak ada DDL** — schema cuma di mapping table, tidak ada `CREATE TABLE` yang bisa di-run                         | Tambah DDL lengkap untuk source dan target            |
| 2   | **`nama_lengkap` truncation** — `nama_depan`(50) + spasi + `nama_belakang`(50) = 101 chars, target `VARCHAR(100)`   | Truncate ke 100 + log `WARN` per row yang kena        |
| 3   | **NULL handling = zero** — seed data memang tidak NULL, tapi production pasti ada                                   | `COALESCE` di semua concat field                      |
| 4   | **`alamat_lengkap` mapping kontradiksi** — teks bilang "copy `alamat` saja", tapi tabel mapping bilang concat semua | Commit ke satu keputusan: copy `alamat` saja          |
| 5   | **Dokter scope tidak eksplisit** — `seed_emr.sql` di project structure imply dokter, tapi plan cuma bahas pasien    | Eksplisit: Phase 1 = pasien only                      |
| 6   | **Worker pool transform bisa break ordering** — fan-out ke N workers = batch order tidak guaranteed                 | Simplify: transform in-place per batch, bukan fan-out |

---

## 1. DDL — Source Database (EMR)

Ini schema **yang sudah ada** di `database_emr`. Kita **tidak create** ini — ini read-only source.

```sql
-- database_emr

CREATE TABLE IF NOT EXISTS pasien (
    id_pasien         INT PRIMARY KEY,
    nama_depan        VARCHAR(50),
    nama_belakang     VARCHAR(50),
    tanggal_lahir     DATE,
    jenis_kelamin     VARCHAR(10),    -- 'Laki-laki' | 'Perempuan'
    email             VARCHAR(100),
    no_telepon        VARCHAR(20),
    alamat            VARCHAR(200),
    kota              VARCHAR(50),
    provinsi          VARCHAR(50),
    kode_pos          VARCHAR(10),
    golongan_darah    VARCHAR(5),     -- 'A+','B+','AB+','O+','A-','B-','AB-','O-'
    kontak_darurat    VARCHAR(100),
    no_kontak_darurat VARCHAR(20),
    tanggal_registrasi DATE
);

-- Volume: 2,000,000 rows (seeded via generate_series)

CREATE TABLE IF NOT EXISTS dokter (
    id_dokter         INT PRIMARY KEY,
    nama_depan        VARCHAR(50),
    nama_belakang     VARCHAR(50),
    spesialisasi      VARCHAR(100),
    email             VARCHAR(100),
    no_telepon        VARCHAR(20),
    no_sip            VARCHAR(20),
    tahun_pengalaman  INT,
    departemen        VARCHAR(50),
    tanggal_bergabung DATE
);

-- Volume: 10,000 rows (Phase 2, tidak di-migrate sekarang)
```

---

## 2. DDL — Target Database (SIMRS)

Ini yang **harus kita create** di `database_simrs`. App akan auto-create kalau belum ada.

```sql
-- database_simrs

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS pasien (
    pasien_uuid            UUID PRIMARY KEY,
    nama_lengkap           VARCHAR(100)  NOT NULL,
    tanggal_lahir          DATE,
    gender                 VARCHAR(10),
    email                  VARCHAR(100),
    telepon                VARCHAR(20),
    alamat_lengkap         VARCHAR(255),
    kota                   VARCHAR(50),
    provinsi               VARCHAR(50),
    kode_pos               VARCHAR(10),
    golongan_darah         VARCHAR(5),
    nama_kontak_darurat    VARCHAR(100),
    telepon_kontak_darurat VARCHAR(20),
    tanggal_registrasi     DATE
);

-- Index untuk query umum SIMRS
CREATE INDEX IF NOT EXISTS idx_pasien_nama ON pasien (nama_lengkap);
CREATE INDEX IF NOT EXISTS idx_pasien_tanggal_registrasi ON pasien (tanggal_registrasi);
```

**Keputusan constraint**:

- `pasien_uuid` = PK (NOT NULL implicit)
- `nama_lengkap` = NOT NULL (pasien tanpa nama = data sampah)
- Semua field lain = NULLABLE (real-world data bisa incomplete)
- Tidak ada `UNIQUE(email)` — satu email bisa dipakai beberapa pasien (anak pakai email orang tua, dll)

---

## 3. Architecture

```
┌──────────────┐                                          ┌──────────────┐
│ PostgreSQL   │    ┌──────────────────────────────┐      │ PostgreSQL   │
│ database_emr │───▶│       Go Migration App       │─────▶│database_simrs│
│ (READ ONLY)  │    │                              │      │ (WRITE)      │
│              │    │  ┌────────────────────────┐  │      │              │
│  pasien      │    │  │ ETL Pipeline (per batch)│  │      │  pasien      │
│  2M rows     │    │  │                        │  │      │  UUID PK     │
│              │    │  │ 1. FETCH 5000 rows     │  │      │              │
│              │    │  │ 2. Transform in-place   │  │      │              │
│              │    │  │ 3. COPY → temp table    │  │      │              │
│              │    │  │ 4. UPSERT from temp     │  │      │              │
│              │    │  │ 5. Update checkpoint    │  │      │              │
│              │    │  └────────────────────────┘  │      │              │
│              │    │                              │      │              │
│              │    │  ┌────────┐  ┌───────────┐  │      │              │
│              │    │  │ HTTP   │  │ Progress  │  │      │              │
│              │    │  │ API    │◀─│ Tracker   │  │      │              │
│              │    │  └────────┘  └───────────┘  │      │              │
└──────────────┘    └──────────────────────────────┘      └──────────────┘
```

### Kenapa TIDAK fan-out worker pool untuk transform?

v1 proposed N workers consuming from channel. **Ini over-engineering untuk kasus ini:**

- Transform per-row itu **CPU-trivial** — string concat + UUID generate = ~microseconds
- Bottleneck ada di **I/O** (DB read/write), bukan CPU
- Fan-out bikin ordering hilang → checkpoint jadi complex
- Simple loop `for _, row := range batch { transform(&row) }` sudah cukup

**Concurrency yang benar**: satu goroutine untuk ETL pipeline, HTTP server di goroutine terpisah. Selesai. Jangan bikin concurrent kalau bottleneck-nya bukan di situ.

---

## 4. Field Mapping & Transformation Rules

### 4.1 Mapping Table

| #   | Source (EMR)                   | Target (SIMRS)           | Rule                                                                                                   |
| --- | ------------------------------ | ------------------------ | ------------------------------------------------------------------------------------------------------ |
| 1   | `id_pasien` INT                | `pasien_uuid` UUID       | UUID v5 dari `NAMESPACE + strconv.Itoa(id_pasien)`                                                     |
| 2   | `nama_depan` + `nama_belakang` | `nama_lengkap`           | `strings.TrimSpace(COALESCE(nama_depan,"") + " " + COALESCE(nama_belakang,""))`, truncate ke 100 chars |
| 3   | `tanggal_lahir`                | `tanggal_lahir`          | Direct copy                                                                                            |
| 4   | `jenis_kelamin`                | `gender`                 | Direct copy                                                                                            |
| 5   | `email`                        | `email`                  | Direct copy                                                                                            |
| 6   | `no_telepon`                   | `telepon`                | Direct copy                                                                                            |
| 7   | `alamat`                       | `alamat_lengkap`         | Direct copy (lihat 4.2)                                                                                |
| 8   | `kota`                         | `kota`                   | Direct copy                                                                                            |
| 9   | `provinsi`                     | `provinsi`               | Direct copy                                                                                            |
| 10  | `kode_pos`                     | `kode_pos`               | Direct copy                                                                                            |
| 11  | `golongan_darah`               | `golongan_darah`         | Direct copy                                                                                            |
| 12  | `kontak_darurat`               | `nama_kontak_darurat`    | Direct copy                                                                                            |
| 13  | `no_kontak_darurat`            | `telepon_kontak_darurat` | Direct copy                                                                                            |
| 14  | `tanggal_registrasi`           | `tanggal_registrasi`     | Direct copy                                                                                            |

### 4.2 Keputusan: `alamat_lengkap`

**Commit**: `alamat_lengkap` = copy `alamat` saja.

**Alasan**: Target sudah punya kolom `kota`, `provinsi`, `kode_pos` terpisah. Kalau `alamat_lengkap` adalah concat dari semua, data akan **terduplikasi** — `kota` ada di kolom `kota` DAN di dalam `alamat_lengkap`. Itu denormalisasi yang tidak perlu dan bikin update jadi inkonsisten.

Source `alamat` (VARCHAR 200) → Target `alamat_lengkap` (VARCHAR 255). **Aman, tidak perlu truncate.**

### 4.3 UUID v5 — Deterministic Generation

```
Namespace: custom UUID (e.g., "a1b2c3d4-e5f6-7890-abcd-ef1234567890")
Input:     strconv.Itoa(id_pasien)
Output:    uuid.NewSHA1(namespace, []byte("12345")) → deterministic UUID

Contoh:
  id_pasien=1     → pasien_uuid = "c4a760a8-..." (selalu sama setiap run)
  id_pasien=2     → pasien_uuid = "7f3d9b2e-..." (selalu sama setiap run)
```

**Property kunci:**

- **Idempotent** — run 2x, UUID sama, upsert aman
- **Traceable** — bisa re-compute `pasien_uuid` dari `id_pasien` kapan saja
- **Collision-free** — SHA1 namespace + unique int = unique UUID (dalam satu namespace)

### 4.4 Edge Cases & Validation Rules

| Field                                      | Risk                                        | Handling                                                |
| ------------------------------------------ | ------------------------------------------- | ------------------------------------------------------- |
| `nama_depan` = NULL                        | `NULL + " " + nama_belakang` = partial name | `COALESCE(nama_depan, "")`                              |
| `nama_belakang` = NULL                     | Same                                        | `COALESCE(nama_belakang, "")`                           |
| `nama_depan` + `nama_belakang` > 100 chars | Truncation — 50+1+50 = 101 max possible     | Truncate ke 100 + log `WARN` dengan `id_pasien`         |
| Kedua nama NULL                            | `nama_lengkap` = `""` (empty string)        | Log `ERROR`, skip row (NOT NULL constraint akan reject) |
| `email` > 100 chars                        | Unlikely tapi possible                      | Truncate ke 100 + log `WARN`                            |
| `alamat` > 255 chars                       | Source max 200, target 255 — safe           | Tidak perlu handling                                    |
| `jenis_kelamin` value unexpected           | Selain 'Laki-laki'/'Perempuan'              | Direct copy, biarkan SIMRS yang validate                |

---

## 5. ETL Pipeline Detail

### 5.1 Extract — Server-Side Cursor

```
BEGIN (READ ONLY, REPEATABLE READ)
  DECLARE migration_cursor CURSOR FOR
    SELECT * FROM pasien
    WHERE id_pasien > $last_checkpoint_id
    ORDER BY id_pasien ASC

  loop:
    FETCH 5000 FROM migration_cursor → batch[]
    if len(batch) == 0 → break
    transform(batch)
    load(batch)
    checkpoint(batch[last].id_pasien)
COMMIT
```

**Kenapa `REPEATABLE READ`?**

- Kalau EMR masih aktif (INSERT/UPDATE saat migrasi jalan), `READ COMMITTED` bisa baca data yang berubah mid-migration
- `REPEATABLE READ` = snapshot at transaction start → consistent view
- Trade-off: long-running transaction, tapi untuk read-only cursor ini acceptable

**Kenapa `ORDER BY id_pasien ASC`?**

- Checkpoint butuh ordering yang predictable
- Resume dari `WHERE id_pasien > last_id` hanya works kalau data terurut

### 5.2 Transform — In-Place per Batch

```go
func transformBatch(batch []EmrPasien) []SimrsPasien {
    result := make([]SimrsPasien, 0, len(batch))
    for _, src := range batch {
        dst, err := transformRow(src)
        if err != nil {
            // log WARN, increment failed counter, skip row
            continue
        }
        result = append(result, dst)
    }
    return result
}
```

Tidak perlu goroutine. Transform per-row = ~1μs. 5000 rows = ~5ms. Bottleneck bukan di sini.

### 5.3 Load — COPY + Upsert via Temp Table

```sql
-- Step 1: Create temp table (no constraints, same structure as target)
CREATE TEMP TABLE _tmp_pasien (LIKE pasien INCLUDING NOTHING) ON COMMIT DROP;

-- Step 2: COPY batch into temp table (pgx.CopyFrom — binary protocol, blazing fast)
COPY _tmp_pasien FROM STDIN (FORMAT binary);

-- Step 3: Upsert from temp into real table
INSERT INTO pasien (pasien_uuid, nama_lengkap, tanggal_lahir, gender, email,
                    telepon, alamat_lengkap, kota, provinsi, kode_pos,
                    golongan_darah, nama_kontak_darurat, telepon_kontak_darurat,
                    tanggal_registrasi)
SELECT pasien_uuid, nama_lengkap, tanggal_lahir, gender, email,
       telepon, alamat_lengkap, kota, provinsi, kode_pos,
       golongan_darah, nama_kontak_darurat, telepon_kontak_darurat,
       tanggal_registrasi
FROM _tmp_pasien
ON CONFLICT (pasien_uuid) DO UPDATE SET
    nama_lengkap           = EXCLUDED.nama_lengkap,
    tanggal_lahir          = EXCLUDED.tanggal_lahir,
    gender                 = EXCLUDED.gender,
    email                  = EXCLUDED.email,
    telepon                = EXCLUDED.telepon,
    alamat_lengkap         = EXCLUDED.alamat_lengkap,
    kota                   = EXCLUDED.kota,
    provinsi               = EXCLUDED.provinsi,
    kode_pos               = EXCLUDED.kode_pos,
    golongan_darah         = EXCLUDED.golongan_darah,
    nama_kontak_darurat    = EXCLUDED.nama_kontak_darurat,
    telepon_kontak_darurat = EXCLUDED.telepon_kontak_darurat,
    tanggal_registrasi     = EXCLUDED.tanggal_registrasi;
```

**Kenapa COPY + temp table, bukan batch INSERT langsung?**

- `COPY` protocol = binary, no SQL parsing per-row = **10-50x faster** than individual INSERTs
- `COPY` tidak support `ON CONFLICT` → solusi: temp table sebagai staging
- Temp table `ON COMMIT DROP` = auto-cleanup, no leftover garbage

### 5.4 Checkpoint — DB-Based (Schema Terpisah di Target Database)

Checkpoint disimpan di tabel `migration_jobs` di **target database** (SIMRS), tapi di **schema `migration`** — bukan di `public` schema bareng data pasien.

```sql
-- Tabel ini auto-created oleh app di database_simrs, schema migration
CREATE SCHEMA IF NOT EXISTS migration;

CREATE TABLE IF NOT EXISTS migration.migration_jobs (
    job_id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_table       VARCHAR(100) NOT NULL,     -- 'pasien', 'dokter', dll
    target_table       VARCHAR(100) NOT NULL,     -- 'pasien'
    status             VARCHAR(20)  NOT NULL DEFAULT 'pending',
                                                  -- pending|running|paused|completed|failed|rolled_back
    total_records      BIGINT       DEFAULT 0,
    processed          BIGINT       DEFAULT 0,
    success            BIGINT       DEFAULT 0,
    failed             BIGINT       DEFAULT 0,
    last_processed_id  BIGINT       DEFAULT 0,    -- checkpoint cursor position
    first_processed_id BIGINT       DEFAULT 0,    -- untuk rollback: start range
    batch_size         INT          NOT NULL DEFAULT 5000,
    batch_delay_ms     INT          NOT NULL DEFAULT 0,  -- backpressure: ms delay antar batch
    dry_run            BOOLEAN      NOT NULL DEFAULT false,
    error_log          JSONB        DEFAULT '[]'::jsonb,
    started_at         TIMESTAMPTZ,
    completed_at       TIMESTAMPTZ,
    rolled_back_at     TIMESTAMPTZ,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Deduplication: tidak bisa create job untuk source+target yang sama
-- kalau masih ada yang pending/running
CREATE UNIQUE INDEX IF NOT EXISTS idx_migration_jobs_active_unique
    ON migration.migration_jobs (source_table, target_table)
    WHERE status IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS idx_migration_jobs_status ON migration.migration_jobs (status);
CREATE INDEX IF NOT EXISTS idx_migration_jobs_source ON migration.migration_jobs (source_table);
```

**Kenapa DB, bukan file?**

| File JSON                              | DB Table                                                         |
| -------------------------------------- | ---------------------------------------------------------------- |
| Container restart + no volume = hilang | Persisted di DB yang sudah di-volume mount                       |
| Satu file = satu migrasi               | Multi-row = multi-job, multi-table                               |
| Tidak bisa query history               | `SELECT * FROM migration.migration_jobs ORDER BY created_at`     |
| Tidak atomic dengan data load          | Checkpoint UPDATE + batch UPSERT bisa dalam **satu transaction** |
| Tidak bisa concurrent job              | Setiap job punya `job_id` sendiri                                |

**Checkpoint update per batch:**

```sql
-- Dalam transaction yang sama dengan UPSERT batch:
UPDATE migration.migration_jobs SET
    processed = processed + $1,
    success = success + $2,
    failed = failed + $3,
    last_processed_id = $4,
    updated_at = NOW()
WHERE job_id = $5;
```

Karena checkpoint dan data load di **database yang sama** (target), bisa di-wrap dalam satu transaction. Kalau batch commit berhasil, checkpoint pasti terupdate. Kalau rollback, checkpoint juga rollback. **True atomicity — tidak ada state inconsistency.**

### 5.5 Mapping Table — Persistent ID Relationship

Tabel `migration.emr_simrs_id_map` menyimpan relasi `id_pasien` (INT, EMR) ↔ `pasien_uuid` (UUID, SIMRS) secara persistent.

```sql
CREATE TABLE IF NOT EXISTS migration.emr_simrs_id_map (
    source_id    BIGINT      NOT NULL,  -- id_pasien dari EMR
    target_uuid  UUID        NOT NULL,  -- pasien_uuid di SIMRS
    source_table VARCHAR(50) NOT NULL,  -- 'pasien'
    job_id       UUID        NOT NULL REFERENCES migration.migration_jobs(job_id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_table, source_id)
);

CREATE INDEX IF NOT EXISTS idx_id_map_target ON migration.emr_simrs_id_map (target_uuid);
CREATE INDEX IF NOT EXISTS idx_id_map_job ON migration.emr_simrs_id_map (job_id);
```

**Kenapa bukan rely UUID v5 saja?**

UUID v5 memang deterministic — `UUID v5(namespace, "12345")` selalu hasilkan UUID yang sama. **Tapi** itu bergantung pada **namespace yang tidak berubah**:

1. **Namespace berubah** (typo, rotasi, developer lain pakai namespace beda) → semua UUID berubah → relasi hilang → rollback jadi impossible
2. **Migrasi dari sumber kedua** (RS merger, import dari sistem lain) → mereka juga punya `id_pasien=12345` tapi beda orang → UUID v5 bentrok
3. **Audit/compliance** → "pasien UUID `abc-123` berasal dari EMR id berapa, dimigrasikan kapan?" → query langsung dari mapping table, tanpa compute

**Insertion**: Di-INSERT **dalam transaksi yang sama** dengan data batch + checkpoint. Setiap batch, setelah UPSERT data pasien, COPY juga mapping rows ke `emr_simrs_id_map`. Zero extra round-trip.

```sql
-- Dalam transaction yang sama dengan batch UPSERT + checkpoint:
INSERT INTO migration.emr_simrs_id_map (source_id, target_uuid, source_table, job_id)
SELECT id_pasien, pasien_uuid, 'pasien', $job_id
FROM _tmp_pasien
ON CONFLICT (source_table, source_id) DO NOTHING;
```

**Cost**: ~50MB untuk 2M rows (2 kolom angka + 1 UUID). Negligible dibanding benefit.

**Rollback integration**: Saat rollback job, mapping rows untuk job tersebut juga di-DELETE:

```sql
DELETE FROM migration.emr_simrs_id_map WHERE job_id = $job_id;
```

### 5.6 Graceful Shutdown (OS Signal Handling)

Ketika proses menerima `SIGTERM` atau `SIGINT` (docker stop, Ctrl+C, kill):

```
OS Signal (SIGTERM/SIGINT)
       │
       ▼
┌─ signal.NotifyContext ──────────────────────────┐
│                                                  │
│  1. ctx.Done() terdeteksi di batch loop          │
│  2. Finish current batch yang sedang jalan        │
│     (jangan interrupt di tengah COPY/UPSERT)     │
│  3. COMMIT batch terakhir + checkpoint            │
│  4. UPDATE job SET status = 'paused'              │
│  5. Close source cursor + COMMIT source tx        │
│  6. Log "migration paused at id=XXXXX"           │
│  7. Close DB connection pools                     │
│  8. HTTP server.Shutdown(timeout 5s)              │
│  9. os.Exit(0)                                    │
│                                                  │
└──────────────────────────────────────────────────┘
```

**Kenapa ini penting?**

- Tanpa graceful shutdown: `docker stop` → `SIGTERM` → proses mati → batch di tengah jalan → checkpoint tidak update → data sudah masuk tapi checkpoint tidak tahu → re-run = duplicate processing (aman karena upsert, tapi wasted work)
- Dengan graceful shutdown: batch terakhir selesai clean, checkpoint terupdate, bisa resume exact dari `last_processed_id`

**Implementasi**: `signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)` → context cancel propagate ke batch loop via `ctx.Err() != nil` check.

### 5.7 Job Deduplication — Prevent Double Migration

Masalah: user accidentally hit `POST /api/jobs` dua kali untuk `source_table=pasien` → dua job jalan parallel → race condition pada target table.

**Solusi: Partial UNIQUE Index**

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_migration_jobs_active_unique
    ON migration.migration_jobs (source_table, target_table)
    WHERE status IN ('pending', 'running');
```

**Behavior:**

- `INSERT` job baru dengan `source_table=pasien, target_table=pasien` → berhasil (pertama kali)
- `INSERT` lagi dengan `source_table=pasien, target_table=pasien` saat job pertama masih `pending`/`running` → **constraint violation** → app return `409 Conflict`
- Setelah job pertama selesai (`completed`/`failed`/`paused`/`rolled_back`) → bisa create job baru

**Kenapa partial unique index, bukan app-level check?**

- App-level check punya race condition: dua request cek "belum ada" secara bersamaan → dua-duanya lolos → duplikat
- DB constraint = **atomic**, tidak ada race condition. PostgreSQL guarantee.

### 5.8 Backpressure — Jangan Kill Source DB

Kalau source database (EMR) masih serve production traffic, migration tool yang FETCH 5000 rows nonstop bisa:

- Bikin connection pool source penuh
- Increase I/O latency untuk query production lain
- Worst case: source DB jadi lambat/unresponsive

**Solusi: Configurable delay antar batch**

```
Batch 1: FETCH → transform → load → checkpoint
    ↓
  time.Sleep(batch_delay_ms)   ← backpressure
    ↓
Batch 2: FETCH → transform → load → checkpoint
    ↓
  time.Sleep(batch_delay_ms)
    ↓
  ...
```

- `BATCH_DELAY_MS=0` (default) → no delay, full speed (untuk local dev / off-peak)
- `BATCH_DELAY_MS=100` → 100ms pause antar batch. 400 batches × 100ms = +40 detik total. Acceptable trade-off.
- `BATCH_DELAY_MS=500` → aggressive throttle untuk shared production DB
- Bisa di-set per-job via `POST /api/jobs` body (`batch_delay_ms` field)

**Kenapa bukan rate limiter (token bucket / leaky bucket)?**

- Overkill. Simple `time.Sleep` per batch sudah cukup untuk one-time migration tool.
- Rate limiter add complexity (goroutine, ticker, refill logic) tanpa proportional benefit.
- Kalau perlu lebih sophisticated, upgrade ke rate limiter = trivial refactor (ganti `time.Sleep` dengan `limiter.Wait()`).

### 5.9 Rollback / Undo Migration

Kalau data yang sudah masuk ke SIMRS ternyata salah (bug di transform, wrong mapping, data corrupt), harus bisa **undo**.

**Strategi: DELETE by UUID range dari job**

Setiap job menyimpan `first_processed_id` dan `last_processed_id`. Karena UUID v5 deterministic dari `id_pasien`, kita bisa re-generate semua UUID yang dihasilkan oleh job ini dan DELETE.

```sql
-- Rollback: hapus semua data yang dimigrasikan oleh job ini
-- Re-generate UUID v5 dari range id_pasien yang di-process oleh job
DELETE FROM public.pasien
WHERE pasien_uuid IN (
    SELECT uuid_v5_generate(namespace, id::text)
    FROM generate_series(
        $first_processed_id,
        $last_processed_id
    ) AS id
);

-- Update job status
UPDATE migration.migration_jobs SET
    status = 'rolled_back',
    rolled_back_at = NOW(),
    updated_at = NOW()
WHERE job_id = $job_id;
```

**Di application layer (Go):**

```
POST /api/jobs/:job_id/rollback

1. Load job → get first_processed_id, last_processed_id
2. Validate: status harus 'completed' atau 'failed' (tidak bisa rollback yang sedang running)
3. Generate UUID v5 untuk setiap id di range [first, last]
4. DELETE FROM public.pasien WHERE pasien_uuid IN (batch UUIDs)
   — dilakukan per-batch (5000 per DELETE) untuk avoid long lock
5. UPDATE job status → 'rolled_back'
6. Log total rows deleted
```

**Kenapa approach ini?**

| Approach                     | Pro                                                                                           | Kontra                                                                            |
| ---------------------------- | --------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| **DELETE by UUID range** ✅  | Tidak perlu kolom tambahan di target. UUID v5 re-computable. Per-batch DELETE = no long lock. | Harus re-generate UUID di app. Kalau namespace berubah, rollback jadi impossible. |
| Simpan `source_id` di target | Query langsung tanpa re-generate                                                              | Polusi target schema dengan kolom yang bukan domain data                          |
| TRUNCATE seluruh target      | Simple                                                                                        | Hapus data pasien yang bukan dari migration ini (kalau ada data manual)           |

**Constraint:**

- Rollback hanya bisa untuk job yang `completed` atau `failed`. Tidak bisa rollback job `running` (harus stop dulu).
- Rollback **tidak reversible** — setelah rollback, data hilang dari target. Re-run migration kalau mau balik.
- Job yang sudah `rolled_back` tidak bisa di-start ulang. Harus create job baru.

### 5.10 Testing Strategy

#### Unit Tests (Table-Driven)

Fokus di `transformer.go` — satu-satunya tempat logic non-trivial:

```go
// test cases untuk transformRow()
var testCases = []struct {
    name     string
    input    EmrPasien
    expected SimrsPasien
    wantErr  bool
}{
    {"happy path",          EmrPasien{NamaDepan: "Budi", NamaBelakang: "Santoso"}, SimrsPasien{NamaLengkap: "Budi Santoso"}, false},
    {"nama_depan NULL",     EmrPasien{NamaDepan: "", NamaBelakang: "Santoso"},     SimrsPasien{NamaLengkap: "Santoso"}, false},
    {"kedua nama NULL",     EmrPasien{NamaDepan: "", NamaBelakang: ""},             SimrsPasien{}, true},
    {"truncation 101 chars", EmrPasien{NamaDepan: strings.Repeat("a",50), NamaBelakang: strings.Repeat("b",50)}, SimrsPasien{NamaLengkap: /* 100 chars */}, false},
    {"UUID determinism",    EmrPasien{IdPasien: 12345}, SimrsPasien{PasienUUID: expectedUUID}, false},
    {"UUID idempotent",     EmrPasien{IdPasien: 12345}, SimrsPasien{PasienUUID: expectedUUID}, false}, // same input = same output
}
```

Juga test untuk:

- `job_repository.go` — CRUD operations, status transitions
- `validator.go` — count matching logic

#### Integration Tests (`testcontainers-go`)

Spin up 2 PostgreSQL containers → seed → run full migration → assert:

```
1. Seed 100 rows ke source EMR container
2. Run migration (full pipeline: extract → transform → load → checkpoint)
3. Assert:
   a. COUNT(target) == COUNT(source) - COUNT(failed)
   b. Random sample 10 rows → field values match transform rules
   c. Checkpoint updated correctly (last_processed_id == max source id)
   d. UUID determinism: re-run → no new rows, all UPDATEd
   e. Mapping table: 100 rows in emr_simrs_id_map
   f. Rollback: POST rollback → COUNT(target) == 0, job status == 'rolled_back'
   g. Deduplication: create same job again while running → 409 Conflict
   h. Graceful stop: send stop signal → job status == 'paused', checkpoint saved
```

**Kenapa `testcontainers-go` bukan mock?**

- Migration tool = 90% DB interaction. Mocking DB = testing your mocks, bukan testing your code.
- `testcontainers-go` spin up real PostgreSQL dalam Docker, ~3-5 detik. Worth it.
- Test output = **proof** yang bisa di-demo: `go test -v ./...` → semua hijau.

---

## 6. Monitoring & Control API

API di-design **resource-oriented** (RESTful) dengan `job_id` sebagai resource identity. Setiap migrasi = satu job. Jelas apa yang sedang di-migrate, bisa jalan parallel, bisa query history.

### 6.1 Endpoints

| Method | Path                         | Description                               |
| ------ | ---------------------------- | ----------------------------------------- |
| `POST` | `/api/jobs`                  | Buat migration job baru (return `job_id`) |
| `POST` | `/api/jobs/:job_id/start`    | Mulai/resume job                          |
| `POST` | `/api/jobs/:job_id/stop`     | Stop graceful (selesaikan batch, pause)   |
| `POST` | `/api/jobs/:job_id/rollback` | Undo migration — DELETE data dari target  |
| `GET`  | `/api/jobs`                  | List semua jobs (history + active)        |
| `GET`  | `/api/jobs/:job_id`          | Detail + progress real-time satu job      |
| `GET`  | `/api/jobs/:job_id/errors`   | List rows yang gagal untuk job ini        |
| `GET`  | `/api/jobs/:job_id/validate` | Post-migration validation                 |
| `GET`  | `/api/tables`                | List table yang sudah di-support          |

### 6.2 Create Job Request

```bash
POST /api/jobs
Content-Type: application/json

{
  "source_table": "pasien",
  "target_table": "pasien",
  "batch_size": 5000,
  "batch_delay_ms": 100,
  "dry_run": false
}
```

Response:

```json
{
  "job_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "status": "pending",
  "source_table": "pasien",
  "target_table": "pasien",
  "batch_size": 5000,
  "dry_run": false,
  "created_at": "2026-03-29T10:00:00Z"
}
```

**Error: Table Tidak Didukung (422)**

Kalau `source_table` atau `target_table` tidak ada di registry:

```json
HTTP 422 Unprocessable Entity
{
  "error": "unsupported_table",
  "message": "source_table 'dokter' is not supported yet. Add transformer and target DDL first.",
  "supported_tables": ["pasien"]
}
```

**Kenapa 422, bukan 400?**

- `400 Bad Request` = format request salah (field missing, type wrong)
- `422 Unprocessable Entity` = request valid secara format, tapi data tidak bisa diproses oleh business logic. Ini kasus yang tepat.

**`GET /api/tables` Response**

```json
{
  "supported": [
    {
      "source_table": "pasien",
      "target_table": "pasien",
      "description": "2M rows, INT PK → UUID PK, field merge nama"
    }
  ]
}
```

```bash
GET /api/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479
```

```json
{
  "job_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "source_table": "pasien",
  "target_table": "pasien",
  "status": "running",
  "total_records": 2000000,
  "processed": 1250000,
  "success": 1249980,
  "failed": 20,
  "percentage": 62.5,
  "elapsed": "3m24s",
  "eta": "2m02s",
  "throughput_rps": 6127,
  "current_batch": 250,
  "total_batches": 400,
  "last_processed_id": 1250000,
  "batch_size": 5000,
  "dry_run": false,
  "started_at": "2026-03-29T10:00:00Z",
  "created_at": "2026-03-29T10:00:00Z"
}
```

### 6.4 List Jobs (History)

```bash
GET /api/jobs?status=completed
```

```json
{
  "jobs": [
    {
      "job_id": "f47ac10b-...",
      "source_table": "pasien",
      "status": "completed",
      "total_records": 2000000,
      "success": 1999980,
      "failed": 20,
      "started_at": "2026-03-29T10:00:00Z",
      "completed_at": "2026-03-29T10:02:34Z"
    }
  ],
  "total": 1
}
```

### 6.5 Validation Response

```json
{
  "job_id": "f47ac10b-...",
  "source_table": "pasien",
  "source_count": 2000000,
  "target_count": 1999980,
  "missing": 20,
  "match": false
}
```

### 6.6 Kenapa Design Ini?

| Sebelumnya (`/api/migration/*`)    | Sekarang (`/api/jobs/:job_id/*`)                          |
| ---------------------------------- | --------------------------------------------------------- |
| Tidak tahu _apa_ yang dimigrasikan | `source_table` + `target_table` eksplisit di setiap job   |
| Satu migrasi at a time             | Multi-job, bisa run pasien dan dokter terpisah            |
| Tidak ada history                  | `GET /api/jobs` = audit trail semua job yang pernah jalan |
| Stop = kill process                | `POST /api/jobs/:id/stop` = graceful pause, bisa resume   |

---

## 7. Project Structure

```
migration-db/
├── main.go                       # Entry point: init config, DB pools, start HTTP server
├── go.mod
├── go.sum
│
├── config/
│   └── config.go                 # Struct + load from ENV (source/target DSN, dll)
│
├── model/
│   ├── emr.go                    # EMR pasien struct (source)
│   ├── simrs.go                  # SIMRS pasien struct (target)
│   └── job.go                    # MigrationJob struct (checkpoint + status)
│
├── migration/
│   ├── migrator.go               # Core ETL loop: extract → transform → load → checkpoint
│   ├── migrator_test.go          # Integration tests (testcontainers-go)
│   ├── extractor.go              # Server-side cursor, FETCH batches
│   ├── transformer.go            # Row-by-row field mapping + validation
│   ├── transformer_test.go       # Unit tests (table-driven: happy, NULL, truncation, UUID)
│   ├── loader.go                 # COPY + temp table + upsert
│   ├── validator.go              # Pre/post migration count validation
│   └── registry.go               # Table registry: map[source_table] → TransformerFunc. 422 kalau tidak ada.
│
├── repository/
│   ├── job_repository.go         # CRUD migration.migration_jobs (checkpoint persistence)
│   └── mapping_repository.go     # CRUD migration.emr_simrs_id_map (ID mapping persistence)
│
├── api/
│   ├── router.go                 # Route registration (/api/jobs/...)
│   └── handler.go                # HTTP handlers (create job, start, stop, status, errors)
│
├── monitoring/
│   └── tracker.go                # Thread-safe progress counter (atomic ops, in-memory)
│
├── scripts/
│   ├── 001_init_emr.sql          # CREATE TABLE + seed 2M pasien + 10K dokter (for local dev)
│   └── 002_init_simrs.sql        # CREATE TABLE pasien (public) + migration.migration_jobs + emr_simrs_id_map
│
├── docker-compose.yml            # postgres_emr + postgres_simrs + app
├── Dockerfile                    # Multi-stage build
├── .env.example                  # Template environment variables
├── Makefile                      # dev shortcuts (up, down, migrate, seed, test)
└── README.md                     # How to run
```

**Perubahan dari v1 → v2:**

- `checkpoint.go` dihapus → diganti `repository/job_repository.go` (DB-based, bukan file)
- Tambah `model/job.go` — struct `MigrationJob` yang merepresentasikan satu migration job
- API handler berubah total — dari static `/migration` ke resource-oriented `/api/jobs/:job_id`
- SQL script `002_init_simrs.sql` sekarang juga create `migration_jobs` table

---

## 8. Tech Stack

| Component | Choice                      | Kenapa                                                             |
| --------- | --------------------------- | ------------------------------------------------------------------ |
| Language  | Go 1.22+                    | Goroutine, static binary, memory predictable                       |
| DB Driver | `jackc/pgx/v5`              | Native PG wire protocol, `CopyFrom()`, connection pooling built-in |
| HTTP      | `net/http` stdlib           | 5 endpoint, tidak perlu framework                                  |
| Logging   | `rs/zerolog`                | Zero-alloc, structured JSON, level-based                           |
| Config    | `kelseyhightower/envconfig` | 12-factor, parse ENV ke struct                                     |
| UUID      | `google/uuid`               | `uuid.NewSHA1()` untuk UUID v5                                     |
| Container | Docker Compose              | 2 PG instances + app, satu command                                 |
| Testing   | `testcontainers-go`         | Real PostgreSQL containers untuk integration test, bukan mock      |

**Yang TIDAK dipakai dan kenapa:**

- **GORM / sqlx** — ORM tidak bisa `CopyFrom()`, kita butuh raw PG protocol
- **Gin / Echo / Fiber** — 5 endpoint tidak justify dependency HTTP framework
- **Kafka / NATS** — one-time migration, bukan event streaming
- **goose / golang-migrate** — ini data migration, bukan schema migration

---

## 9. Execution Flow

```
┌─ main.go ──────────────────────────────────────────────┐
│                                                         │
│  1. Load config dari ENV                                │
│  2. signal.NotifyContext(SIGTERM, SIGINT) → ctx         │
│  3. Connect source DB pool (pgxpool)                    │
│  4. Connect target DB pool (pgxpool)                    │
│  5. Auto-create target tables + migration schema/table  │
│  6. Init progress tracker                               │
│  7. Start HTTP server (goroutine)                       │
│  8. Wait for POST /api/jobs/:job_id/start               │
│                                                         │
│  ┌─ Migration Goroutine (per job) ──────────────────┐  │
│  │                                                    │  │
│  │  9.  Dedup check (partial unique index)            │  │
│  │  10. Load job → first_id, last_id                  │  │
│  │  11. COUNT(*) from source → update job.total       │  │
│  │  12. UPDATE job SET status = 'running'             │  │
│  │  13. BEGIN READ ONLY REPEATABLE READ (source)      │  │
│  │  14. DECLARE CURSOR WHERE id > last_id             │  │
│  │                                                    │  │
│  │  ┌─ Batch Loop ──────────────────────────────┐    │  │
│  │  │  15. Check ctx.Done() → graceful exit      │    │  │
│  │  │  16. FETCH batch_size → batch[]            │    │  │
│  │  │  17. if empty → break                      │    │  │
│  │  │  18. transformBatch(batch) → simrs[]       │    │  │
│  │  │  19. BEGIN (target)                        │    │  │
│  │  │      a. CREATE TEMP TABLE                  │    │  │
│  │  │      b. COPY FROM (binary)                 │    │  │
│  │  │      c. INSERT...ON CONFLICT DO UPDATE     │    │  │
│  │  │      d. UPDATE checkpoint + first_id       │    │  │
│  │  │      COMMIT (data + checkpoint atomic)     │    │  │
│  │  │  20. Update in-memory tracker (atomic)     │    │  │
│  │  │  21. Check stop signal → break if stopped  │    │  │
│  │  │  22. time.Sleep(batch_delay_ms)            │    │  │
│  │  └───────────────────────────────────────────┘    │  │
│  │                                                    │  │
│  │  23. COMMIT (close source cursor)                  │  │
│  │  24. Run validation                                │  │
│  │  25. UPDATE job SET status = 'completed'           │  │
│  └────────────────────────────────────────────────────┘  │
│                                                         │
│  On ctx.Done():                                         │
│    → finish current batch → status='paused'             │
│    → server.Shutdown(5s) → close pools → exit(0)        │
└─────────────────────────────────────────────────────────┘
```

---

## 10. Performance Estimates

| Metric              | Value                                                                        |
| ------------------- | ---------------------------------------------------------------------------- |
| Total rows          | 2,000,000                                                                    |
| Batch size          | 5,000                                                                        |
| Total batches       | 400                                                                          |
| Transform per batch | ~5ms (CPU, trivial)                                                          |
| COPY per batch      | ~20-50ms (network + disk)                                                    |
| Upsert per batch    | ~30-80ms                                                                     |
| **Total per batch** | **~55-135ms**                                                                |
| **Total migration** | **~22s - 54s** (optimistic) / **~2-3 min** (conservative, includes overhead) |
| Memory footprint    | ~30-50MB (hanya batch active + buffers)                                      |
| Checkpoint interval | Setiap batch (setiap 5000 rows)                                              |

---

## 11. Error Handling Strategy

### Prinsip: **Skip & Log, Bukan Fail-Fast**

Kenapa? 1 row rusak dari 2 juta tidak boleh menghentikan seluruh migrasi. Log, skip, report setelah selesai.

| Error Type                                    | Action                                                                                     |
| --------------------------------------------- | ------------------------------------------------------------------------------------------ |
| Row transform gagal (e.g., nama kosong)       | Log `WARN`, skip row, increment `failed` counter                                           |
| Batch load gagal (e.g., constraint violation) | Retry 3x dengan backoff, kalau tetap gagal → log seluruh batch, skip, lanjut               |
| DB connection lost                            | Retry connect dengan exponential backoff (max 5x), kalau gagal → set status `failed`, stop |
| Checkpoint write gagal                        | Log `ERROR`, tapi jangan stop — worst case, batch terakhir akan di-re-process (idempotent) |

### Error Log Entry

```json
{
  "level": "warn",
  "id_pasien": 34521,
  "field": "nama_lengkap",
  "error": "truncated from 101 to 100 chars",
  "original_value": "Muhammad Abdurrahman ... Firmansyah",
  "timestamp": "2026-03-29T10:05:23Z"
}
```

---

## 12. Docker Compose Setup

```yaml
services:
  # ─── Local DB (hanya untuk development) ───────────────────
  # Jalankan dengan: docker compose --profile local up --build
  # JANGAN pakai di production/VPS — pakai external DB langsung

  postgres_emr:
    image: postgres:16-alpine
    profiles: ["local"]
    environment:
      POSTGRES_DB: ${EMR_DB_NAME:-database_emr}
      POSTGRES_USER: ${EMR_DB_USER:-emr_user}
      POSTGRES_PASSWORD: ${EMR_DB_PASSWORD:-emr_pass}
    ports: ["${EMR_DB_PORT:-5433}:5432"]
    volumes:
      - ./scripts/001_init_emr.sql:/docker-entrypoint-initdb.d/001_init.sql
      - emr_data:/var/lib/postgresql/data

  postgres_simrs:
    image: postgres:16-alpine
    profiles: ["local"]
    environment:
      POSTGRES_DB: ${SIMRS_DB_NAME:-database_simrs}
      POSTGRES_USER: ${SIMRS_DB_USER:-simrs_user}
      POSTGRES_PASSWORD: ${SIMRS_DB_PASSWORD:-simrs_pass}
    ports: ["${SIMRS_DB_PORT:-5434}:5432"]
    volumes:
      - ./scripts/002_init_simrs.sql:/docker-entrypoint-initdb.d/001_init.sql
      - simrs_data:/var/lib/postgresql/data

  # ─── Migration App ───────────────────────────────────────
  # Selalu jalan. SOURCE_DSN dan TARGET_DSN dari .env
  # Bisa point ke local PG containers ATAU external VPS DB

  migration:
    build: .
    environment:
      SOURCE_DSN: ${SOURCE_DSN}
      TARGET_DSN: ${TARGET_DSN}
      BATCH_SIZE: ${BATCH_SIZE:-5000}
      BATCH_DELAY_MS: ${BATCH_DELAY_MS:-0}
      HTTP_PORT: ${HTTP_PORT:-8080}
      LOG_LEVEL: ${LOG_LEVEL:-info}
    ports: ["${HTTP_PORT:-8080}:${HTTP_PORT:-8080}"]

volumes:
  emr_data:
  simrs_data:
```

### Cara Pakai

**Mode 1: Local development** (spin up 2 PG + app)

```bash
cp .env.example .env
# Edit .env kalau perlu
docker compose --profile local up --build
```

**Mode 2: External DB / VPS** (app only, point ke DB di VPS)

```bash
# .env
SOURCE_DSN=postgres://user:pass@vps-emr.example.com:5432/database_emr?sslmode=require
TARGET_DSN=postgres://user:pass@vps-simrs.example.com:5432/database_simrs?sslmode=require

docker compose up --build
# Tanpa --profile local → PG containers TIDAK di-spin up
```

**Kenapa `profiles`?**

- Tanpa profile flag → hanya `migration` service yang jalan
- Dengan `--profile local` → kedua PG containers juga ikut jalan
- Satu `docker-compose.yml` untuk dua scenario, bukan dua file

---

## 13. Configuration (Environment Variables)

| Variable         | Default  | Description                                                      |
| ---------------- | -------- | ---------------------------------------------------------------- |
| `SOURCE_DSN`     | required | PostgreSQL connection string untuk database_emr                  |
| `TARGET_DSN`     | required | PostgreSQL connection string untuk database_simrs                |
| `BATCH_SIZE`     | `5000`   | Default jumlah rows per FETCH (bisa di-override per job via API) |
| `BATCH_DELAY_MS` | `0`      | Default delay antar batch dalam ms (backpressure, 0 = no delay)  |
| `HTTP_PORT`      | `8080`   | Port untuk monitoring API                                        |
| `LOG_LEVEL`      | `info`   | zerolog level (debug/info/warn/error)                            |

> **Note**: Checkpoint di tabel `migration.migration_jobs` (schema `migration` di target DB, bukan `public`).
> `DRY_RUN`, `BATCH_SIZE`, dan `BATCH_DELAY_MS` bisa di-override per-job (saat `POST /api/jobs`).

---

## 14. Scope Boundaries

### ✅ Phase 1 (Sekarang — MVP)

- [x] Migrasi tabel `pasien` EMR → SIMRS (2M rows)
- [x] ETL pipeline: cursor → transform → COPY+upsert
- [x] Deterministic UUID v5
- [x] DB-based checkpoint/resume (atomic with data load)
- [x] Job-based REST API (`/api/jobs/:job_id/...`)
- [x] Docker Compose (2 PG + app, `.env`-based, profile support)
- [x] Structured logging (zerolog)
- [x] Idempotent (safe re-run via upsert)
- [x] **Graceful shutdown** — `SIGTERM`/`SIGINT` → finish batch → save checkpoint → clean exit
- [x] **Job deduplication** — partial unique index, prevent double migration untuk data yang sama
- [x] **Backpressure** — configurable `batch_delay_ms` per-job, protect source DB
- [x] **Rollback/undo** — `POST /api/jobs/:job_id/rollback` → DELETE migrated data by UUID range
- [x] **Mapping table** — `migration.emr_simrs_id_map` persist `id_pasien ↔ pasien_uuid`, atomic with batch
- [x] **Unit + Integration Tests** — table-driven unit tests + `testcontainers-go` integration tests

### 🔲 Phase 2 (Future Improvements)

Ini belum diimplementasi. Documented supaya reviewer tahu kita aware — dan supaya saat interview kamu bisa bilang "saya sudah pikirin, ini trade-off-nya" bukan "oh iya belum kepikiran."

---

#### Tier 1 — Harus Ada Kalau Ini Production (Interviewer Pasti Tanya)

| #   | Improvement               | Kenapa Penting                                                                                                                                                                 | Effort |
| --- | ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------ |
| 1   | **Health Check Endpoint** | `GET /healthz` dan `GET /readyz`. Biar `docker compose` bisa pakai `healthcheck` dan app tunggu sampai kedua PG benar-benar ready (`SELECT 1` berhasil, bukan cuma port open). | Small  |
| 2   | **Dry-Run Mode**          | Jalankan seluruh pipeline tanpa write ke target. Untuk validasi mapping dan edge case sebelum real migration. Set `dry_run: true` di create job request.                       | Small  |

> **Note**: Rollback, Graceful Shutdown, Backpressure, Job Deduplication, Mapping Table, dan Tests sudah **masuk Phase 1** — bukan improvement lagi.

---

#### Tier 2 — Naikkan Nilai, Tunjukkan Senior-Level Thinking

| #   | Improvement                                         | Kenapa Penting                                                                                                                                                                          | Effort |
| --- | --------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------ |
| 3   | **Migrasi Tabel `dokter`**                          | 10K rows, butuh DDL target SIMRS dari tim produk. Arsitekturnya sudah scalable — tinggal buat `DokterTransformer` yang implement interface yang sama. Tunjukkan design kamu extensible. | Small  |
| 4   | **Observability Stack (Prometheus + Grafana)**      | Expose `/metrics`: `migration_rows_processed_total`, `migration_batch_duration_seconds`, `migration_errors_total`. Ini bedakan "backend engineer" dari "senior backend engineer."       | Medium |
| 5   | **Parallel Pipeline (Bukan Fan-Out)**               | Pipeline parallelism: saat batch N di-load ke target, batch N+1 sudah di-extract dari source. Dua goroutine, satu channel. Potong total time ~30-40%.                                   | Medium |
| 6   | **Data Validation Post-Migration yang Lebih Dalam** | Sekarang cuma COUNT match. Tambah: (a) random sample 1000 rows → compare field-by-field, (b) checksum agregat untuk detect silent corruption.                                           | Medium |

---

#### Tier 3 — Nice-to-Have, Tunjukkan Kamu Mikir Jauh

| #   | Improvement                         | Kenapa Penting                                                                                                                           | Effort |
| --- | ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- | ------ |
| 7   | **Data Anonymization Mode**         | Mask PII saat migrasi ke staging/dev. Relevan untuk **UU Perlindungan Data Pribadi** (PDP).                                              | Medium |
| 8   | **WebSocket Live Progress**         | Upgrade dari polling `GET /status` ke WebSocket push. Nice-to-have — polling tiap 1 detik sudah cukup.                                   | Medium |
| 9   | **CI/CD Pipeline**                  | GitHub Actions: lint → test → build → push. Tunjukkan kamu mikirin delivery pipeline.                                                    | Small  |
| 10  | **Error Report CSV Export**         | `GET /api/jobs/:job_id/errors?format=csv` — download failed rows sebagai CSV untuk manual review.                                        | Small  |
| 11  | **Multi-Table Migration Framework** | Abstract ETL pipeline jadi interface: `Extractor`, `Transformer`, `Loader`. Setiap tabel implement interface. Extensible tanpa refactor. | Medium |
| 12  | **Incremental / Delta Migration**   | Re-run migration untuk data baru — idempotent by design (upsert). UUID v5 bikin ini gratis secara logic.                                 | Small  |

---

#### Prioritas untuk Interview Prep

```
SUDAH DI PHASE 1 (langsung demo, bukan cuma bicara):
  ✅ Rollback         — "POST /api/jobs/:id/rollback, DELETE by UUID range"
  ✅ Signal handling   — "SIGTERM → finish batch → checkpoint → exit"
  ✅ Backpressure      — "batch_delay_ms configurable per-job"
  ✅ Job dedup         — "partial unique index, 409 Conflict kalau double create"
  ✅ Mapping table     — "emr_simrs_id_map, persistent audit trail, atomic with batch"
  ✅ Tests             — "table-driven unit tests + testcontainers-go integration"

MUST DISCUSS (improvement yang pasti ditanya):
  #1 Health check    — "gimana app tahu DB ready?"
  #2 Dry-run         — "gimana test tanpa affect production?"

SHOULD MENTION (naikkan impression):
  #4  Observability  — tunjukkan production mindset
  #6  Deep validation — tunjukkan data integrity awareness
  #11 Framework      — tunjukkan system design skill

NICE TO DROP (kalau ada kesempatan):
  #7  Anonymization  — compliance awareness
  #12 Delta migration — forward-thinking
```

---

## 14b. Kenapa Design Ini Scalable (Improvement yang Sudah "Gratis")

Design Phase 1 **tidak sempurna** — tapi sudah **scalable by design**. Beberapa improvement yang biasa butuh refactor besar, di sini tinggal extend:

| Yang sudah gratis         | Kenapa                                                                                                            |
| ------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| **Multi-table migration** | Setiap job punya `source_table` sendiri. Tambah migrasi `dokter` = create job baru, bukan refactor code           |
| **Audit trail**           | `SELECT * FROM migration.migration_jobs ORDER BY created_at` — history otomatis, schema terpisah dari domain data |
| **Resume setelah crash**  | Checkpoint di DB yang sudah di-volume mount. Container restart → `last_processed_id` masih ada                    |
| **Safe re-run**           | UUID v5 deterministic + upsert = run 10x hasilnya sama                                                            |
| **Monitoring per-job**    | `GET /api/jobs/:id` sudah per-job. Tambah job baru = monitoring otomatis ikut                                     |
| **Rollback per-job**      | `POST /api/jobs/:id/rollback` → undo satu job tanpa affect job lain. UUID range-based, surgical delete            |
| **Safe from double-run**  | Partial unique index di DB. Bukan app-level check yang bisa race condition                                        |
| **ID traceability**       | Mapping table `emr_simrs_id_map` = persistent audit trail. Namespace-independent, query-ready                     |
| **Tested & proven**       | `go test -v ./...` = proof. Unit tests untuk logic, integration tests untuk full pipeline                         |
| **Extensible per-table**  | `registry.go` = tambah table baru tinggal register satu entry. Tidak perlu ubah core ETL loop                     |

---

## 15. Kenapa Arsitektur Ini dan Bukan yang Lain

### "Kenapa tidak pakai `pg_dump` + `pg_restore`?"

Schema berbeda. Bukan copy 1:1. Ada field merge, type change (INT→UUID), rename kolom. `pg_dump` tidak bisa transform.

### "Kenapa tidak Foreign Data Wrapper (FDW) + INSERT...SELECT?"

Bisa, tapi:

1. Tidak ada checkpoint/resume — kalau gagal di tengah, mulai ulang
2. Tidak ada monitoring/progress — query berjalan di background tanpa visibility
3. Transform complex (UUID v5, string concat) lebih natural di application code
4. Tidak portable — terikat ke PostgreSQL-specific feature

### "Kenapa tidak stream langsung (pub/sub)?"

Ini **one-time migration**, bukan CDC (Change Data Capture). Setelah migrasi selesai, tool ini dibuang. Kafka/NATS = infrastruktur permanen untuk problem temporary.

### "Kenapa tidak pakai ETL tool (Apache NiFi, Airbyte, dbt)?"

Requirement bilang **"buat backend Golang."** Selain itu, tool ETL off-the-shelf overkill untuk satu tabel. Setup time > coding time.

---

## 16. Decision Log

| #   | Decision                                            | Alternative Considered                 | Why This                                                                                                         |
| --- | --------------------------------------------------- | -------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| 1   | UUID v5 (deterministic)                             | UUID v4 (random)                       | Idempotent, traceable, resumable                                                                                 |
| 2   | `alamat_lengkap` = copy `alamat` only               | Concat all address fields              | Target sudah punya `kota`, `provinsi`, `kode_pos` terpisah — avoid denormalization                               |
| 3   | COPY + temp table + upsert                          | Batch INSERT with VALUES               | COPY 10-50x faster, upsert via ON CONFLICT tetap possible via staging                                            |
| 4   | Transform in-place (no goroutine pool)              | Fan-out N workers                      | Transform = CPU trivial (~μs/row), bottleneck = I/O, concurrency adds complexity tanpa benefit                   |
| 5   | DB-based checkpoint (target DB, schema `migration`) | File JSON / Redis / DB terpisah        | Atomic dengan data load, schema separation dari domain data, survive container restart                           |
| 6   | `REPEATABLE READ` isolation                         | `READ COMMITTED`                       | Consistent snapshot kalau EMR masih aktif saat migrasi                                                           |
| 7   | `pgx/v5` raw                                        | GORM / sqlx                            | Butuh `CopyFrom()`, direct PG protocol. ORM abstraction = bottleneck                                             |
| 8   | `net/http` stdlib                                   | Gin / Echo                             | 5 endpoint. Framework overhead = unjustified dependency                                                          |
| 9   | Graceful shutdown (`signal.NotifyContext`)          | Tidak ada / defer ke Phase 2           | Production basic hygiene. Tanpa ini, SIGTERM = data inconsistency potential. Effort kecil, impact besar          |
| 10  | Partial unique index (job dedup)                    | App-level check before insert          | DB constraint = atomic, no race condition. App-level check bisa di-bypass oleh concurrent requests               |
| 11  | `time.Sleep` backpressure                           | Token bucket / rate limiter            | Simple, adequate untuk one-time migration. Rate limiter = over-engineering                                       |
| 12  | DELETE by UUID range (rollback)                     | TRUNCATE / simpan source_id di target  | TRUNCATE too destructive. Extra column polusi target schema. UUID re-generation = zero storage cost              |
| 13  | Mapping table (`emr_simrs_id_map`)                  | Rely UUID v5 saja                      | UUID v5 bergantung namespace tetap. Mapping table = insurance + audit trail. ~50MB cost = negligible             |
| 14  | `testcontainers-go` integration tests               | Mock DB / SQLite                       | Migration = 90% DB interaction. Mocking DB = testing mocks. Real PG containers = real proof                      |
| 15  | Table registry (`registry.go`) + 422                | Hardcode pasien / panic / silent wrong | Unsupported table = product blocker, bukan technical bug. 422 = explicit feedback. Registry = satu titik extend. |

---

## 17. Interview Cheat Sheet — Pertanyaan yang Pasti Muncul

Ini bukan "ready to code" section — ini **persiapan perang**.

### Q: "Kenapa batch 5000? Kenapa bukan 1000 atau 50000?"

**A**: Trade-off antara tiga hal:

- **Terlalu kecil (1000)** → overhead per-batch dominan (CREATE TEMP, COPY, UPSERT = 3 round trips per batch). 2000 batches × overhead = lambat.
- **Terlalu besar (50000)** → memory spike (~50K × ~500 bytes/row = ~25MB per batch, masih aman tapi checkpoint granularity berkurang. Kalau crash, re-process 50K rows bukan 5K).
- **5000** = sweet spot: ~2.5MB per batch, 400 batches, checkpoint setiap ~3 detik.
- **Tapi ini configurable via ENV** (`BATCH_SIZE`). Kalau interviewer challenge, bilang "saya bisa tune ini berdasarkan benchmark di environment production."

### Q: "2 juta row itu sebenarnya kecil. Kenapa arsitekturnya segini complex?"

**A**: Jujur? Iya, 2M row bisa selesai dengan satu `INSERT...SELECT` via FDW dalam 30 detik. Tapi:

1. Requirement bilang **"efisien, terukur, dapat dimonitoring"** — FDW tidak punya monitoring.
2. Requirement bilang **"backend Golang"** — bukan SQL script.
3. Arsitektur ini **scalable ke 200M rows** tanpa perubahan. Yang berubah cuma `BATCH_SIZE` dan estimasi waktu.
4. **Real value**: checkpoint/resume, idempotent upsert, error tracking, dry-run. Ini yang bedakan migration tool dari migration script.

### Q: "Gimana kalau data di EMR berubah saat migrasi jalan?"

**A**: `REPEATABLE READ` isolation level pada source transaction. Cursor kita baca snapshot pada saat `BEGIN`. Data yang INSERT/UPDATE setelah transaction start **tidak terlihat**. Konsisten. Tapi artinya data baru setelah migrasi mulai harus di-handle oleh run ulang (delta migration — idempotent by design).

### Q: "Gimana kamu prove data yang di-migrate itu benar?"

**A**: Empat layer (3 sudah diimplementasi):

1. **Automated tests** ✅ — table-driven unit tests untuk transformer (happy path, NULL, truncation, UUID determinism) + integration tests dengan `testcontainers-go` (spin up 2 PG, seed, migrate, assert field values)
2. **Count validation** ✅ — `COUNT(source)` vs `COUNT(target) + COUNT(failed)` harus match
3. **Mapping table** ✅ — `emr_simrs_id_map` bisa di-query untuk verify `id_pasien → pasien_uuid` relationship
4. **Random sample** — ambil 1000 random rows, compare field-by-field (Phase 2)
5. **Checksum** — aggregate hash di source vs target (Phase 2)

### Q: "Kenapa checkpoint di DB, bukan file JSON?"

**A**: Awalnya saya desain file-based, tapi ada 3 masalah fundamental:

1. **Tidak atomic dengan data** — file write dan DB commit adalah dua operasi terpisah. Kalau crash di antara keduanya, state inconsistent (data sudah masuk tapi checkpoint belum update, atau sebaliknya). Dengan DB, checkpoint UPDATE dan data UPSERT dalam **satu transaction**.
2. **Container ephemeral** — Docker container restart tanpa volume mount = file hilang. DB sudah di-volume mount by design.
3. **Multi-job** — file JSON hanya bisa track satu migrasi. DB table bisa track unlimited jobs + history.

Trade-off: sekarang checkpoint punya dependency ke target DB. Tapi target DB sudah jadi dependency anyway — kalau target DB mati, migration juga mati regardless.

### Q: "Kamu bilang `COPY` 10-50x faster. Angka dari mana?"

**A**:

- `INSERT` per-row = 1 SQL parse + 1 network round trip per row. 5000 rows = 5000 round trips.
- `COPY` = 1 binary stream, no SQL parsing, 1 network call. 5000 rows = 1 call.
- Benchmark dari pgx maintainer (jackc): COPY ~100K rows/sec vs INSERT ~5-10K rows/sec pada typical network latency.
- Kita bisa benchmark sendiri — jalankan migration dengan COPY vs batch INSERT, compare `throughput_rps` di status API.

### Q: "Kalau ini production, apa yang kamu ubah?"

**A** (ini jawaban yang bedakan senior dari mid):

Yang **sudah saya implement** di Phase 1:

1. **Signal handling** — `SIGTERM` → graceful shutdown, finish batch, save checkpoint
2. **Backpressure** — `batch_delay_ms` configurable, protect source DB saat production
3. **Rollback** — `POST /api/jobs/:id/rollback` → DELETE by UUID range
4. **Job dedup** — partial unique index, 409 Conflict kalau double create
5. **Mapping table** — `emr_simrs_id_map` untuk audit trail dan namespace-independent traceability
6. **Tests** — table-driven unit tests + integration tests dengan `testcontainers-go`

Yang **belum** tapi sudah ada roadmap: 7. **Health check** — `/healthz`, `/readyz` untuk Kubernetes liveness/readiness probes 8. **Metrics** — Prometheus `migration_rows_total`, `migration_batch_duration_seconds` 9. **Alerting** — kalau `error_rate > 0.1%`, alert ke Slack/PagerDuty

---

## Ready to Code

Phase 1 sekarang bukan cuma "working migration" — tapi production-grade tool dengan: graceful shutdown, job deduplication, backpressure, rollback, mapping table (audit trail), dan tested (unit + integration). Foundation scalable, improvement roadmap jelas. Next: implementasi Go sesuai Section 7.
