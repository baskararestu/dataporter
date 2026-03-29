package migration

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ValidationResult holds the outcome of a post-migration count check.
type ValidationResult struct {
	JobID       uuid.UUID `json:"job_id"`
	SourceTable string    `json:"source_table"`
	SourceCount int64     `json:"source_count"`
	TargetCount int64     `json:"target_count"`
	Missing     int64     `json:"missing"`
	Match       bool      `json:"match"`
}

// Validator compares row counts between source and target after a migration run.
type Validator struct {
	sourceConn *pgx.Conn
	targetDB   *pgxpool.Pool
}

// NewValidator creates a Validator that queries both databases.
func NewValidator(sourceConn *pgx.Conn, targetDB *pgxpool.Pool) *Validator {
	return &Validator{sourceConn: sourceConn, targetDB: targetDB}
}

// Validate counts rows in source and target and returns a comparison result.
// The source count only includes rows that were in scope for this job
// (i.e. id_pasien > first_processed_id - 1, bounded by last_processed_id).
func (v *Validator) Validate(ctx context.Context, jobID uuid.UUID, sourceTable string, firstID, lastID int64) (*ValidationResult, error) {
	var sourceCount int64
	err := v.sourceConn.QueryRow(ctx,
		`SELECT COUNT(*) FROM pasien WHERE id_pasien BETWEEN $1 AND $2`,
		firstID, lastID,
	).Scan(&sourceCount)
	if err != nil {
		return nil, fmt.Errorf("count source rows: %w", err)
	}

	var targetCount int64
	err = v.targetDB.QueryRow(ctx,
		`SELECT COUNT(*) FROM migration.emr_simrs_id_map WHERE job_id = $1 AND source_table = $2`,
		jobID, sourceTable,
	).Scan(&targetCount)
	if err != nil {
		return nil, fmt.Errorf("count target rows via id map: %w", err)
	}

	missing := sourceCount - targetCount
	return &ValidationResult{
		JobID:       jobID,
		SourceTable: sourceTable,
		SourceCount: sourceCount,
		TargetCount: targetCount,
		Missing:     missing,
		Match:       missing == 0,
	}, nil
}
