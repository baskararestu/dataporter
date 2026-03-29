package migration

import (
	"context"
	"fmt"

	"github.com/baskararestu/dataporter/model"
	"github.com/baskararestu/dataporter/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Loader writes a transformed batch into the SIMRS target database.
// It uses COPY → temp table → upsert to maximise throughput while staying idempotent.
type Loader struct {
	db      *pgxpool.Pool
	mapping *repository.MappingRepository
}

// NewLoader creates a Loader backed by the given target DB pool.
func NewLoader(db *pgxpool.Pool, mapping *repository.MappingRepository) *Loader {
	return &Loader{db: db, mapping: mapping}
}

// LoadBatch writes rows to the target in a single transaction:
//  1. Create temp table (dropped automatically at tx end)
//  2. COPY rows into temp table via binary protocol
//  3. Upsert from temp into public.pasien
//  4. Insert ID mapping rows
//  5. Update job checkpoint + counters
//
// The entire operation is atomic — checkpoint and data land together or not at all.
func (l *Loader) LoadBatch(
	ctx context.Context,
	jobID uuid.UUID,
	jobRepo *repository.JobRepository,
	rows []model.SIMRSPasien,
	srcRows []model.EMRPasien,
	dryRun bool,
) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := l.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin target tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if !dryRun {
		// Step 1: temp table — same structure as target, no constraints, auto-dropped at commit.
		if _, err := tx.Exec(ctx, `
			CREATE TEMP TABLE IF NOT EXISTS _tmp_pasien
			(LIKE pasien) ON COMMIT DROP`); err != nil {
			return fmt.Errorf("create temp table: %w", err)
		}

		// Step 2: COPY into temp table via pgx binary protocol.
		if err := l.copyToTemp(ctx, tx, rows); err != nil {
			return err
		}

		// Step 3: upsert from temp into real table.
		if _, err := tx.Exec(ctx, `
			INSERT INTO pasien
				(pasien_uuid, nama_lengkap, tanggal_lahir, gender, email, telepon,
				 alamat_lengkap, kota, provinsi, kode_pos, golongan_darah,
				 nama_kontak_darurat, telepon_kontak_darurat, tanggal_registrasi)
			SELECT pasien_uuid, nama_lengkap, tanggal_lahir, gender, email, telepon,
			       alamat_lengkap, kota, provinsi, kode_pos, golongan_darah,
			       nama_kontak_darurat, telepon_kontak_darurat, tanggal_registrasi
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
				tanggal_registrasi     = EXCLUDED.tanggal_registrasi`); err != nil {
			return fmt.Errorf("upsert pasien: %w", err)
		}

		// Step 4: insert ID mapping entries (idempotent — ON CONFLICT DO NOTHING).
		mapEntries := make([]model.IDMapEntry, len(srcRows))
		for i, src := range srcRows {
			mapEntries[i] = model.IDMapEntry{
				SourceID:    int64(src.IDPasien),
				TargetUUID:  rows[i].PasienUUID,
				SourceTable: "pasien",
				JobID:       jobID,
			}
		}
		if err := l.mapping.BulkInsert(ctx, tx, mapEntries); err != nil {
			return err
		}
	}

	// Step 5: update checkpoint + counters (always, even in dry-run, so progress is visible).
	firstID := int64(srcRows[0].IDPasien)
	lastID := int64(srcRows[len(srcRows)-1].IDPasien)
	if err := jobRepo.UpdateProgress(ctx, jobID,
		int64(len(rows)), int64(len(rows)), 0,
		lastID, firstID,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// copyToTemp bulk-loads transformed rows into _tmp_pasien using pgx CopyFrom (binary protocol).
func (l *Loader) copyToTemp(ctx context.Context, tx pgx.Tx, rows []model.SIMRSPasien) error {
	columns := []string{
		"pasien_uuid", "nama_lengkap", "tanggal_lahir", "gender", "email", "telepon",
		"alamat_lengkap", "kota", "provinsi", "kode_pos", "golongan_darah",
		"nama_kontak_darurat", "telepon_kontak_darurat", "tanggal_registrasi",
	}

	copyRows := make([][]any, len(rows))
	for i, r := range rows {
		copyRows[i] = []any{
			r.PasienUUID, r.NamaLengkap, r.TanggalLahir, r.Gender, r.Email, r.Telepon,
			r.AlamatLengkap, r.Kota, r.Provinsi, r.KodePos, r.GolonganDarah,
			r.NamaKontakDarurat, r.TeleponKontakDarurat, r.TanggalRegistrasi,
		}
	}

	_, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"_tmp_pasien"},
		columns,
		pgx.CopyFromRows(copyRows),
	)
	if err != nil {
		return fmt.Errorf("copy to temp: %w", err)
	}
	return nil
}
