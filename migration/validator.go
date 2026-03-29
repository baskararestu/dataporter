package migration

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VerifyResult holds the outcome of a post-migration consistency check.
type VerifyResult struct {
	JobID        uuid.UUID `json:"job_id"`
	SourceTable  string    `json:"source_table"`
	SourceCount  int64     `json:"source_count"`
	TargetCount  int64     `json:"target_count"`
	Missing      int64     `json:"missing"`
	IsConsistent bool      `json:"is_consistent"`
}

// Validator compares row counts between source and target for a given job range.
type Validator struct {
	sourceConn *pgx.Conn
	targetDB   *pgxpool.Pool
}

// NewValidator creates a Validator that queries both databases.
func NewValidator(sourceConn *pgx.Conn, targetDB *pgxpool.Pool) *Validator {
	return &Validator{sourceConn: sourceConn, targetDB: targetDB}
}

// Verify counts rows in source and target using 2 simple COUNT queries.
//
// Source: COUNT(*) WHERE id_pasien BETWEEN firstID AND lastID
// Target: job.Success + job.Skipped (rows that exist in target = inserted + already existed)
//
// This is O(1) in round-trips regardless of how many rows were migrated.
func (v *Validator) Verify(ctx context.Context, jobID uuid.UUID, sourceTable string, firstID, lastID, jobSuccess, jobSkipped int64) (*VerifyResult, error) {
	var sourceCount int64
	err := v.sourceConn.QueryRow(ctx,
		`SELECT COUNT(*) FROM pasien WHERE id_pasien BETWEEN $1 AND $2`,
		firstID, lastID,
	).Scan(&sourceCount)
	if err != nil {
		return nil, fmt.Errorf("count source rows: %w", err)
	}

	// target count = rows that are definitely in target.
	// inserted (success) + skipped (DO NOTHING = already existed) = total present in target.
	targetCount := jobSuccess + jobSkipped
	missing := sourceCount - targetCount

	return &VerifyResult{
		JobID:        jobID,
		SourceTable:  sourceTable,
		SourceCount:  sourceCount,
		TargetCount:  targetCount,
		Missing:      missing,
		IsConsistent: missing == 0,
	}, nil
}
