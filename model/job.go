package model

import (
	"encoding/json"
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
	JobID            uuid.UUID  `db:"job_id"            json:"job_id"`
	SourceTable      string     `db:"source_table"      json:"source_table"`
	TargetTable      string     `db:"target_table"      json:"target_table"`
	Status           JobStatus  `db:"status"            json:"status"`
	TotalRecords     int64      `db:"total_records"     json:"total_records"`
	Processed        int64      `db:"processed"         json:"processed"`
	Success          int64      `db:"success"           json:"success"`
	Failed           int64      `db:"failed"            json:"failed"`
	LastProcessedID  int64      `db:"last_processed_id"  json:"last_processed_id"`
	FirstProcessedID int64      `db:"first_processed_id" json:"first_processed_id"`
	BatchSize        int        `db:"batch_size"        json:"batch_size"`
	BatchDelayMs     int        `db:"batch_delay_ms"    json:"batch_delay_ms"`
	DryRun           bool             `db:"dry_run"           json:"dry_run"`
	ErrorLog         json.RawMessage  `db:"error_log"         json:"error_log,omitempty"` // JSONB stored as raw bytes
	StartedAt        *time.Time `db:"started_at"        json:"started_at,omitempty"`
	CompletedAt      *time.Time `db:"completed_at"      json:"completed_at,omitempty"`
	RolledBackAt     *time.Time `db:"rolled_back_at"    json:"rolled_back_at,omitempty"`
	CreatedAt        time.Time  `db:"created_at"        json:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"        json:"updated_at"`
}

// CreateJobRequest holds the parameters for creating a new migration job via the API.
type CreateJobRequest struct {
	SourceTable  string `json:"source_table"  example:"pasien"`
	TargetTable  string `json:"target_table"  example:"pasien"`
	BatchSize    int    `json:"batch_size"    example:"5000"`
	BatchDelayMs int    `json:"batch_delay_ms" example:"0"`
	DryRun       bool   `json:"dry_run"       example:"false"`
}

// IDMapEntry represents one row in migration.emr_simrs_id_map.
// Persists the source INT id ↔ target UUID relationship as an audit trail.
type IDMapEntry struct {
	SourceID    int64     `db:"source_id"    json:"source_id"`
	TargetUUID  uuid.UUID `db:"target_uuid"  json:"target_uuid"`
	SourceTable string    `db:"source_table" json:"source_table"`
	JobID       uuid.UUID `db:"job_id"       json:"job_id"`
	CreatedAt   time.Time `db:"created_at"   json:"created_at"`
}

// ErrorResponse is the standard error envelope returned on failures.
type ErrorResponse struct {
	Error   string `json:"error"   example:"invalid_request"`
	Message string `json:"message" example:"source_table and target_table are required"`
}

// StatusResponse is a generic status message response.
type StatusResponse struct {
	JobID  string `json:"job_id"  example:"550e8400-e29b-41d4-a716-446655440000"`
	Status string `json:"status"  example:"starting"`
}

// ListJobsResponse wraps the jobs list with a total count.
type ListJobsResponse struct {
	Jobs  []MigrationJob `json:"jobs"`
	Total int            `json:"total"`
}

// ListTablesResponse lists supported source→target table pairs.
type ListTablesResponse struct {
	Supported []string `json:"supported"`
}

// HealthResponse is returned by the /healthz endpoint.
type HealthResponse struct {
	Status string    `json:"status" example:"ok"`
	Time   time.Time `json:"time"`
}

// JobErrorsResponse wraps the error log for a job.
type JobErrorsResponse struct {
	JobID  string `json:"job_id"`
	Errors []any  `json:"errors"`
	Total  int    `json:"total"`
}
