package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/baskararestu/dataporter/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobRepository handles persistence of migration jobs in migration.migration_jobs.
type JobRepository struct {
	db *pgxpool.Pool
}

// NewJobRepository creates a new JobRepository backed by the given connection pool.
func NewJobRepository(db *pgxpool.Pool) *JobRepository {
	return &JobRepository{db: db}
}

// Create inserts a new migration job with status 'pending' and returns it.
// Returns an error wrapping pgconn.PgError with code 23505 if a duplicate active job exists
// (enforced by idx_migration_jobs_active_unique partial unique index → caller returns 409).
func (r *JobRepository) Create(ctx context.Context, req model.CreateJobRequest) (*model.MigrationJob, error) {
	const q = `
		INSERT INTO migration.migration_jobs
			(source_table, target_table, batch_size, batch_delay_ms, dry_run, last_processed_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING job_id, source_table, target_table, status,
		          total_records, processed, success, failed, skipped,
		          last_processed_id, first_processed_id,
		          batch_size, batch_delay_ms, dry_run, error_log,
		          started_at, completed_at, rolled_back_at, created_at, updated_at`

	row := r.db.QueryRow(ctx, q,
		req.SourceTable, req.TargetTable,
		req.BatchSize, req.BatchDelayMs, req.DryRun, req.StartFromID,
	)
	return scanJob(row)
}

// GetByID retrieves a single migration job by its UUID.
func (r *JobRepository) GetByID(ctx context.Context, jobID uuid.UUID) (*model.MigrationJob, error) {
	const q = `
		SELECT job_id, source_table, target_table, status,
		       total_records, processed, success, failed, skipped,
		       last_processed_id, first_processed_id,
		       batch_size, batch_delay_ms, dry_run, error_log,
		       started_at, completed_at, rolled_back_at, created_at, updated_at
		FROM migration.migration_jobs
		WHERE job_id = $1`

	row := r.db.QueryRow(ctx, q, jobID)
	return scanJob(row)
}

// List retrieves all migration jobs, optionally filtered by status.
// Pass an empty string to return all jobs.
func (r *JobRepository) List(ctx context.Context, status string) ([]*model.MigrationJob, error) {
	var (
		q    string
		args []any
	)
	if status != "" {
		q = `SELECT job_id, source_table, target_table, status,
		            total_records, processed, success, failed, skipped,
		            last_processed_id, first_processed_id,
		            batch_size, batch_delay_ms, dry_run, error_log,
		            started_at, completed_at, rolled_back_at, created_at, updated_at
		     FROM migration.migration_jobs
		     WHERE status = $1
		     ORDER BY created_at DESC`
		args = []any{status}
	} else {
		q = `SELECT job_id, source_table, target_table, status,
		            total_records, processed, success, failed, skipped,
		            last_processed_id, first_processed_id,
		            batch_size, batch_delay_ms, dry_run, error_log,
		            started_at, completed_at, rolled_back_at, created_at, updated_at
		     FROM migration.migration_jobs
		     ORDER BY created_at DESC`
	}

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*model.MigrationJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// UpdateStatus sets the job status and optionally records timestamps.
func (r *JobRepository) UpdateStatus(ctx context.Context, jobID uuid.UUID, status model.JobStatus) error {
	now := time.Now()
	var err error
	switch status {
	case model.JobStatusRunning:
		_, err = r.db.Exec(ctx,
			`UPDATE migration.migration_jobs SET status=$1, started_at=$2, updated_at=$3 WHERE job_id=$4`,
			status, now, now, jobID)
	case model.JobStatusCompleted:
		_, err = r.db.Exec(ctx,
			`UPDATE migration.migration_jobs SET status=$1, completed_at=$2, updated_at=$3 WHERE job_id=$4`,
			status, now, now, jobID)
	case model.JobStatusRolledBack:
		_, err = r.db.Exec(ctx,
			`UPDATE migration.migration_jobs SET status=$1, rolled_back_at=$2, updated_at=$3 WHERE job_id=$4`,
			status, now, now, jobID)
	default:
		_, err = r.db.Exec(ctx,
			`UPDATE migration.migration_jobs SET status=$1, updated_at=$2 WHERE job_id=$3`,
			status, now, jobID)
	}
	if err != nil {
		return fmt.Errorf("update job status: %w", err)
	}
	return nil
}

// UpdateProgress atomically updates checkpoint and counters for a batch.
// Called within the same transaction as the data upsert for atomicity.
func (r *JobRepository) UpdateProgress(ctx context.Context, jobID uuid.UUID, processed, success, failed, skipped, lastID, firstID int64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE migration.migration_jobs SET
			processed          = processed + $1,
			success            = success   + $2,
			failed             = failed    + $3,
			skipped            = skipped   + $4,
			last_processed_id  = $5,
			first_processed_id = CASE WHEN first_processed_id = 0 THEN $6 ELSE first_processed_id END,
			updated_at         = NOW()
		WHERE job_id = $7`,
		processed, success, failed, skipped, lastID, firstID, jobID,
	)
	if err != nil {
		return fmt.Errorf("update job progress: %w", err)
	}
	return nil
}

// SetTotalRecords writes the source row count to the job after the initial COUNT query.
func (r *JobRepository) SetTotalRecords(ctx context.Context, jobID uuid.UUID, total int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE migration.migration_jobs SET total_records=$1, updated_at=NOW() WHERE job_id=$2`,
		total, jobID)
	if err != nil {
		return fmt.Errorf("set total records: %w", err)
	}
	return nil
}

// GetLatestCompleted returns the most recent completed job for a source+target pair.
// Used by CreateJob to detect when a migration has already been fully processed.
func (r *JobRepository) GetLatestCompleted(ctx context.Context, sourceTable, targetTable string) (*model.MigrationJob, error) {
	const q = `
		SELECT job_id, source_table, target_table, status,
		       total_records, processed, success, failed, skipped,
		       last_processed_id, first_processed_id,
		       batch_size, batch_delay_ms, dry_run, error_log,
		       started_at, completed_at, rolled_back_at, created_at, updated_at
		FROM migration.migration_jobs
		WHERE source_table = $1 AND target_table = $2 AND status = 'completed'
		ORDER BY created_at DESC
		LIMIT 1`

	row := r.db.QueryRow(ctx, q, sourceTable, targetTable)
	return scanJob(row)
}

// AppendError adds a structured error entry to the job's error_log JSONB array.
func (r *JobRepository) AppendError(ctx context.Context, jobID uuid.UUID, entry any) error {
	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal error entry: %w", err)
	}
	_, err = r.db.Exec(ctx,
		`UPDATE migration.migration_jobs SET error_log = error_log || $1::jsonb, updated_at=NOW() WHERE job_id=$2`,
		string(b), jobID)
	if err != nil {
		return fmt.Errorf("append job error: %w", err)
	}
	return nil
}

// scanner is satisfied by both pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanJob(s scanner) (*model.MigrationJob, error) {
	var j model.MigrationJob
	err := s.Scan(
		&j.JobID, &j.SourceTable, &j.TargetTable, &j.Status,
		&j.TotalRecords, &j.Processed, &j.Success, &j.Failed, &j.Skipped,
		&j.LastProcessedID, &j.FirstProcessedID,
		&j.BatchSize, &j.BatchDelayMs, &j.DryRun, &j.ErrorLog,
		&j.StartedAt, &j.CompletedAt, &j.RolledBackAt, &j.CreatedAt, &j.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan job: %w", err)
	}
	return &j, nil
}
