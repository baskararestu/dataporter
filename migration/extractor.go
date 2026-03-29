package migration

import (
	"context"
	"fmt"

	"github.com/baskararestu/dataporter/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Extractor reads batches of EMR pasien rows using a server-side cursor.
// The cursor runs inside a REPEATABLE READ transaction to guarantee a consistent
// snapshot even if the source DB is still serving production traffic.
//
// NewExtractor acquires a dedicated connection from the pool for the lifetime of the
// cursor — server-side cursors are connection-scoped and cannot be shared.
type Extractor struct {
	conn       *pgxpool.Conn // dedicated connection, released on Close
	tx         pgx.Tx
	cursorName string
	batchSize  int
	done       bool
}

// NewExtractor acquires a dedicated connection from the source pool, opens a REPEATABLE READ
// transaction, and declares a cursor starting after lastProcessedID (for checkpoint resume).
// Also returns the total row count within the same snapshot to avoid TOCTOU mismatch.
//
// selectSQL must be the cursor query body with %d as a placeholder for lastProcessedID —
// pgx does not support parameters in DECLARE ... CURSOR FOR SELECT, so the ID is interpolated
// with fmt.Sprintf before the DECLARE statement is executed.
//
// countSQL must use $1 for the lastProcessedID parameter.
//
// The caller MUST call Close() to release the connection back to the pool.
func NewExtractor(ctx context.Context, pool *pgxpool.Pool, lastProcessedID int64, batchSize int, selectSQL, countSQL string) (*Extractor, int64, error) {
	// Acquire a dedicated connection — cursors are connection-scoped.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("acquire source conn: %w", err)
	}

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		conn.Release()
		return nil, 0, fmt.Errorf("begin source tx: %w", err)
	}

	// Count total INSIDE the snapshot — same transaction, same consistent view as cursor.
	var total int64
	if err := tx.QueryRow(ctx, countSQL, lastProcessedID).Scan(&total); err != nil {
		_ = tx.Rollback(ctx)
		conn.Release()
		return nil, 0, fmt.Errorf("count total: %w", err)
	}

	cursorName := "migration_cursor"
	_, err = tx.Exec(ctx, fmt.Sprintf(
		"DECLARE %s CURSOR FOR %s",
		cursorName,
		fmt.Sprintf(selectSQL, lastProcessedID),
	))
	if err != nil {
		_ = tx.Rollback(ctx)
		conn.Release()
		return nil, 0, fmt.Errorf("declare cursor: %w", err)
	}

	return &Extractor{
		conn:       conn,
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

// Close commits the source transaction (closes the cursor) and releases the dedicated
// connection back to the pool.
func (e *Extractor) Close(ctx context.Context) error {
	defer e.conn.Release()
	if err := e.tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit source tx: %w", err)
	}
	return nil
}
