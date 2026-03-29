package migration

import (
	"context"
	"fmt"

	"github.com/baskararestu/dataporter/model"
	"github.com/jackc/pgx/v5"
)

// Extractor reads batches of EMR pasien rows using a server-side cursor.
// The cursor runs inside a REPEATABLE READ transaction to guarantee a consistent
// snapshot even if the source DB is still serving production traffic.
type Extractor struct {
	tx         pgx.Tx
	cursorName string
	batchSize  int
	done       bool
}

// NewExtractor opens a REPEATABLE READ transaction on the source connection and
// declares a cursor starting after lastProcessedID (for checkpoint resume).
// Also returns the total row count within the same snapshot to avoid TOCTOU mismatch.
func NewExtractor(ctx context.Context, conn *pgx.Conn, lastProcessedID int64, batchSize int) (*Extractor, int64, error) {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("begin source tx: %w", err)
	}

	// Count total INSIDE the snapshot — same transaction, same consistent view as cursor.
	var total int64
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(id_pasien) FROM pasien WHERE id_pasien > $1`, lastProcessedID,
	).Scan(&total); err != nil {
		_ = tx.Rollback(ctx)
		return nil, 0, fmt.Errorf("count total: %w", err)
	}

	cursorName := "migration_cursor"
	_, err = tx.Exec(ctx, fmt.Sprintf(
		`DECLARE %s CURSOR FOR
		 SELECT id_pasien, nama_depan, nama_belakang, tanggal_lahir, jenis_kelamin,
		        email, no_telepon, alamat, kota, provinsi, kode_pos,
		        golongan_darah, kontak_darurat, no_kontak_darurat, tanggal_registrasi
		 FROM pasien
		 WHERE id_pasien > %d
		 ORDER BY id_pasien ASC`,
		cursorName, lastProcessedID,
	))
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, 0, fmt.Errorf("declare cursor: %w", err)
	}

	return &Extractor{
		tx:         tx,
		cursorName: cursorName,
		batchSize:  batchSize,
	}, total, nil
}

// FetchBatch retrieves the next batch of rows from the cursor.
// Returns an empty slice when no more rows are available.
func (e *Extractor) FetchBatch(ctx context.Context) ([]model.EMRPasien, error) {
	if e.done {
		return nil, nil
	}

	rows, err := e.tx.Query(ctx, fmt.Sprintf("FETCH %d FROM %s", e.batchSize, e.cursorName))
	if err != nil {
		return nil, fmt.Errorf("fetch batch: %w", err)
	}
	defer rows.Close()

	var batch []model.EMRPasien
	for rows.Next() {
		var p model.EMRPasien
		if err := rows.Scan(
			&p.IDPasien, &p.NamaDepan, &p.NamaBelakang, &p.TanggalLahir, &p.JenisKelamin,
			&p.Email, &p.NoTelepon, &p.Alamat, &p.Kota, &p.Provinsi, &p.KodePos,
			&p.GolonganDarah, &p.KontakDarurat, &p.NoKontakDarurat, &p.TanggalRegistrasi,
		); err != nil {
			return nil, fmt.Errorf("scan pasien row: %w", err)
		}
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	if len(batch) == 0 {
		e.done = true
	}
	return batch, nil
}

// Close commits the source transaction (closes the cursor) and releases the connection.
func (e *Extractor) Close(ctx context.Context) error {
	if err := e.tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit source tx: %w", err)
	}
	return nil
}
