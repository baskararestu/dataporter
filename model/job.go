package model

import (
	"time"

	"github.com/google/uuid"
)

// JobStatus represents the lifecycle state of a migration job.
type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusRunning    JobStatus = "running"
	JobStatusPaused     JobStatus = "paused"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
	JobStatusRolledBack JobStatus = "rolled_back"
)

// MigrationJob represents a single migration job stored in migration.migration_jobs.
// One job = one source→target table migration run with its own checkpoint, progress, and audit trail.
type MigrationJob struct {
	JobID             uuid.UUID  `db:"job_id"`
	SourceTable       string     `db:"source_table"`
	TargetTable       string     `db:"target_table"`
	Status            JobStatus  `db:"status"`
	TotalRecords      int64      `db:"total_records"`
	Processed         int64      `db:"processed"`
	Success           int64      `db:"success"`
	Failed            int64      `db:"failed"`
	LastProcessedID   int64      `db:"last_processed_id"`
	FirstProcessedID  int64      `db:"first_processed_id"`
	BatchSize         int        `db:"batch_size"`
	BatchDelayMs      int        `db:"batch_delay_ms"`
	DryRun            bool       `db:"dry_run"`
	ErrorLog          []byte     `db:"error_log"` // JSONB stored as raw bytes
	StartedAt         *time.Time `db:"started_at"`
	CompletedAt       *time.Time `db:"completed_at"`
	RolledBackAt      *time.Time `db:"rolled_back_at"`
	CreatedAt         time.Time  `db:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at"`
}

// CreateJobRequest holds the parameters for creating a new migration job via the API.
type CreateJobRequest struct {
	SourceTable  string `json:"source_table"`
	TargetTable  string `json:"target_table"`
	BatchSize    int    `json:"batch_size"`
	BatchDelayMs int    `json:"batch_delay_ms"`
	DryRun       bool   `json:"dry_run"`
}

// IDMapEntry represents one row in migration.emr_simrs_id_map.
// Persists the source INT id ↔ target UUID relationship as an audit trail.
type IDMapEntry struct {
	SourceID    int64     `db:"source_id"`
	TargetUUID  uuid.UUID `db:"target_uuid"`
	SourceTable string    `db:"source_table"`
	JobID       uuid.UUID `db:"job_id"`
	CreatedAt   time.Time `db:"created_at"`
}
