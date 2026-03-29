package repository

import (
	"context"
	"fmt"

	"github.com/baskararestu/dataporter/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MappingRepository handles persistence of the ID mapping table (migration.emr_simrs_id_map).
// It stores the permanent source INT id ↔ target UUID relationship for audit and rollback.
type MappingRepository struct {
	db *pgxpool.Pool
}

func NewMappingRepository(db *pgxpool.Pool) *MappingRepository {
	return &MappingRepository{db: db}
}

// BulkInsert inserts a batch of ID map entries using SendBatch + ON CONFLICT DO NOTHING.
// Safe to re-run (idempotent). Intended to be called within the same transaction as the data batch upsert.
func (r *MappingRepository) BulkInsert(ctx context.Context, tx pgx.Tx, entries []model.IDMapEntry) error {
	if len(entries) == 0 {
		return nil
	}
	return r.bulkInsertDirect(ctx, tx, entries)
}

// bulkInsertDirect uses a single INSERT...ON CONFLICT DO NOTHING for the mapping batch.
// This is the primary insertion path; it is safe to re-run (idempotent).
func (r *MappingRepository) bulkInsertDirect(ctx context.Context, tx pgx.Tx, entries []model.IDMapEntry) error {
	batch := &pgx.Batch{}
	for _, e := range entries {
		batch.Queue(
			`INSERT INTO migration.emr_simrs_id_map (source_id, target_uuid, source_table, job_id)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (source_table, source_id) DO NOTHING`,
			e.SourceID, e.TargetUUID, e.SourceTable, e.JobID,
		)
	}
	br := tx.SendBatch(ctx, batch)
	defer br.Close()

	for range entries {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("bulk insert id map: %w", err)
		}
	}
	return nil
}

// DeleteByJobID removes all mapping entries associated with a given job.
// Called during rollback to clean up the audit trail for the rolled-back job.
func (r *MappingRepository) DeleteByJobID(ctx context.Context, jobID uuid.UUID) (int64, error) {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM migration.emr_simrs_id_map WHERE job_id = $1`,
		jobID,
	)
	if err != nil {
		return 0, fmt.Errorf("delete id map by job: %w", err)
	}
	return tag.RowsAffected(), nil
}

// GetBySourceID looks up the target UUID for a given source ID and table.
// Useful for debugging and validation.
func (r *MappingRepository) GetBySourceID(ctx context.Context, sourceTable string, sourceID int64) (*model.IDMapEntry, error) {
	var e model.IDMapEntry
	err := r.db.QueryRow(ctx,
		`SELECT source_id, target_uuid, source_table, job_id, created_at
		 FROM migration.emr_simrs_id_map
		 WHERE source_table = $1 AND source_id = $2`,
		sourceTable, sourceID,
	).Scan(&e.SourceID, &e.TargetUUID, &e.SourceTable, &e.JobID, &e.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get id map entry: %w", err)
	}
	return &e, nil
}
