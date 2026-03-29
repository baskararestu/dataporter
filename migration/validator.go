package migration

import (
	"context"
	"fmt"
	"strconv"

	"github.com/baskararestu/dataporter/model"
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
// Compares source rows in the job's ID range against actual rows in public.pasien
// by recomputing deterministic UUID v5 in batches.
func (v *Validator) Validate(ctx context.Context, jobID uuid.UUID, sourceTable string, firstID, lastID int64) (*ValidationResult, error) {
	var sourceCount int64
	err := v.sourceConn.QueryRow(ctx,
		`SELECT COUNT(*) FROM pasien WHERE id_pasien BETWEEN $1 AND $2`,
		firstID, lastID,
	).Scan(&sourceCount)
	if err != nil {
		return nil, fmt.Errorf("count source rows: %w", err)
	}

	// Count target rows by checking existence of deterministic UUID v5 in batches.
	const batchSize = 5000
	var targetCount int64

	for start := firstID; start <= lastID; start += batchSize {
		end := start + batchSize - 1
		if end > lastID {
			end = lastID
		}

		uuids := make([]uuid.UUID, 0, end-start+1)
		for id := start; id <= end; id++ {
			uuids = append(uuids, uuid.NewSHA1(model.UUIDNamespace, []byte(strconv.FormatInt(id, 10))))
		}

		var batchCount int64
		err = v.targetDB.QueryRow(ctx,
			`SELECT COUNT(*) FROM public.pasien WHERE pasien_uuid = ANY($1)`,
			uuids,
		).Scan(&batchCount)
		if err != nil {
			return nil, fmt.Errorf("count target rows batch: %w", err)
		}
		targetCount += batchCount
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
