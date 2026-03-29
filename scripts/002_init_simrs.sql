-- ============================================================
-- database_simrs: Target Schema for Migration
-- ============================================================
-- Executed automatically by docker-entrypoint-initdb.d
-- or auto-created by migration app if not exists

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- SIMRS pasien table (migration target from EMR)
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

-- Index for common SIMRS queries
CREATE INDEX IF NOT EXISTS idx_pasien_nama ON pasien (nama_lengkap);
CREATE INDEX IF NOT EXISTS idx_pasien_tanggal_registrasi ON pasien (tanggal_registrasi);

-- ============================================================
-- migration schema: Checkpoint & Job Tracking
-- ============================================================
-- Separate schema from data domain (public.pasien)
-- Used by migration app for:
-- 1. Track progress of each migration job
-- 2. Checkpoint/resume (last_processed_id)
-- 3. Audit trail (history of all migrations)

CREATE SCHEMA IF NOT EXISTS migration;

CREATE TABLE IF NOT EXISTS migration.migration_jobs (
    job_id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_table       VARCHAR(100) NOT NULL,     -- 'pasien', 'dokter', etc
    target_table       VARCHAR(100) NOT NULL,     -- 'pasien'
    status             VARCHAR(20)  NOT NULL DEFAULT 'pending',
                                                  -- pending|running|paused|completed|failed|rolled_back
    total_records      BIGINT       DEFAULT 0,
    processed          BIGINT       DEFAULT 0,
    success            BIGINT       DEFAULT 0,
    failed             BIGINT       DEFAULT 0,
    last_processed_id  BIGINT       DEFAULT 0,    -- checkpoint cursor position
    first_processed_id BIGINT       DEFAULT 0,    -- for rollback: start range
    batch_size         INT          NOT NULL DEFAULT 5000,
    batch_delay_ms     INT          NOT NULL DEFAULT 0,  -- backpressure: ms delay between batches
    dry_run            BOOLEAN      NOT NULL DEFAULT false,
    error_log          JSONB        DEFAULT '[]'::jsonb,
    skipped            BIGINT       DEFAULT 0,    -- rows already existed in target (DO NOTHING)
    started_at         TIMESTAMPTZ,
    completed_at       TIMESTAMPTZ,
    rolled_back_at     TIMESTAMPTZ,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Deduplication: prevent concurrent or duplicate migration for same source+target.
-- 'completed' is included so a finished job blocks a new one (use rollback first to re-run).
CREATE UNIQUE INDEX IF NOT EXISTS idx_migration_jobs_active_unique
    ON migration.migration_jobs (source_table, target_table)
    WHERE status IN ('pending', 'running', 'completed');

CREATE INDEX IF NOT EXISTS idx_migration_jobs_status ON migration.migration_jobs (status);
CREATE INDEX IF NOT EXISTS idx_migration_jobs_source ON migration.migration_jobs (source_table);

-- Mapping table: persistent ID relationship (audit trail)
CREATE TABLE IF NOT EXISTS migration.emr_simrs_id_map (
    source_id    BIGINT      NOT NULL,  -- id_pasien from EMR
    target_uuid  UUID        NOT NULL,  -- pasien_uuid in SIMRS
    source_table VARCHAR(50) NOT NULL,  -- 'pasien'
    job_id       UUID        NOT NULL REFERENCES migration.migration_jobs(job_id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_table, source_id)
);

CREATE INDEX IF NOT EXISTS idx_id_map_target ON migration.emr_simrs_id_map (target_uuid);
CREATE INDEX IF NOT EXISTS idx_id_map_job ON migration.emr_simrs_id_map (job_id);

-- Verification
SELECT 'SIMRS pasien table created (public schema)' AS info;
SELECT 'migration_jobs table created (migration schema)' AS info;
SELECT 'emr_simrs_id_map table created (migration schema)' AS info;
