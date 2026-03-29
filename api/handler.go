package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/baskararestu/dataporter/migration"
	"github.com/baskararestu/dataporter/model"
	"github.com/baskararestu/dataporter/monitoring"
	"github.com/baskararestu/dataporter/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Handler holds all HTTP handler dependencies.
type Handler struct {
	jobRepo     *repository.JobRepository
	mappingRepo *repository.MappingRepository
	migrator    *migration.Migrator
	tracker     *monitoring.Tracker
	sourceConn  *pgx.Conn
	targetDB    *pgxpool.Pool
}

// NewHandler creates a new API handler.
func NewHandler(
	jobRepo *repository.JobRepository,
	mappingRepo *repository.MappingRepository,
	migrator *migration.Migrator,
	tracker *monitoring.Tracker,
	sourceConn *pgx.Conn,
	targetDB *pgxpool.Pool,
) *Handler {
	return &Handler{
		jobRepo:     jobRepo,
		mappingRepo: mappingRepo,
		migrator:    migrator,
		tracker:     tracker,
		sourceConn:  sourceConn,
		targetDB:    targetDB,
	}
}

// CreateJob creates a new migration job in pending state.
func (h *Handler) CreateJob(w http.ResponseWriter, r *http.Request) {
	var req model.CreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.SourceTable == "" || req.TargetTable == "" {
		jsonError(w, http.StatusBadRequest, "invalid_request", "source_table and target_table are required")
		return
	}
	if _, err := migration.LookupTable(req.SourceTable); err != nil {
		jsonError(w, http.StatusUnprocessableEntity, "unsupported_table", err.Error())
		return
	}
	job, err := h.jobRepo.Create(r.Context(), req)
	if err != nil {
		if strings.Contains(err.Error(), "23505") {
			jsonError(w, http.StatusConflict, "duplicate_job", "an active job for this source+target already exists")
			return
		}
		jsonError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	jsonOK(w, http.StatusCreated, job)
}

// StartJob begins (or resumes) a migration job in a background goroutine.
func (h *Handler) StartJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := h.jobRepo.GetByID(r.Context(), jobID)
	if err != nil {
		jsonError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	if job.Status == model.JobStatusRunning {
		jsonError(w, http.StatusConflict, "already_running", "job is already running")
		return
	}
	if job.Status == model.JobStatusCompleted || job.Status == model.JobStatusRolledBack {
		jsonError(w, http.StatusConflict, "terminal_state", "job is in a terminal state and cannot be restarted")
		return
	}
	go func() {
		if err := h.migrator.Run(context.Background(), job); err != nil {
			log.Error().Str("job_id", jobID.String()).Err(err).Msg("migration goroutine error")
		}
	}()
	jsonOK(w, http.StatusAccepted, map[string]string{"job_id": jobID.String(), "status": "starting"})
}

// StopJob signals the running job to pause.
func (h *Handler) StopJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := h.jobRepo.GetByID(r.Context(), jobID)
	if err != nil {
		jsonError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	if job.Status != model.JobStatusRunning {
		jsonError(w, http.StatusConflict, "not_running", "job is not running")
		return
	}
	if err := h.jobRepo.UpdateStatus(r.Context(), jobID, model.JobStatusPaused); err != nil {
		jsonError(w, http.StatusInternalServerError, "stop_failed", err.Error())
		return
	}
	jsonOK(w, http.StatusOK, map[string]string{"job_id": jobID.String(), "status": "paused"})
}

// RollbackJob deletes all migrated data for the job and marks it rolled_back.
func (h *Handler) RollbackJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	if err := h.migrator.Rollback(r.Context(), jobID); err != nil {
		if strings.Contains(err.Error(), "cannot rollback") || strings.Contains(err.Error(), "already rolled back") {
			jsonError(w, http.StatusConflict, "rollback_conflict", err.Error())
			return
		}
		jsonError(w, http.StatusInternalServerError, "rollback_failed", err.Error())
		return
	}
	jsonOK(w, http.StatusOK, map[string]string{"job_id": jobID.String(), "status": "rolled_back"})
}

// ListJobs returns all jobs, optionally filtered by ?status=.
func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	jobs, err := h.jobRepo.List(r.Context(), status)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	jsonOK(w, http.StatusOK, map[string]any{"jobs": jobs, "total": len(jobs)})
}

// GetJob returns full details and live progress for a single job.
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := h.jobRepo.GetByID(r.Context(), jobID)
	if err != nil {
		jsonError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	processed, success, failed := h.tracker.Get(jobID)
	total := h.tracker.GetTotal(jobID)

	type jobResponse struct {
		*model.MigrationJob
		LiveProcessed int64   `json:"live_processed,omitempty"`
		LiveSuccess   int64   `json:"live_success,omitempty"`
		LiveFailed    int64   `json:"live_failed,omitempty"`
		Percentage    float64 `json:"percentage,omitempty"`
	}
	resp := jobResponse{MigrationJob: job}
	if job.Status == model.JobStatusRunning && total > 0 {
		resp.LiveProcessed = processed
		resp.LiveSuccess = success
		resp.LiveFailed = failed
		resp.Percentage = float64(processed) / float64(total) * 100
	}
	jsonOK(w, http.StatusOK, resp)
}

// ValidateJob runs a post-migration count check for the job.
func (h *Handler) ValidateJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := h.jobRepo.GetByID(r.Context(), jobID)
	if err != nil {
		jsonError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	validator := migration.NewValidator(h.sourceConn, h.targetDB)
	result, err := validator.Validate(r.Context(), jobID, job.SourceTable, job.FirstProcessedID, job.LastProcessedID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "validate_failed", err.Error())
		return
	}
	jsonOK(w, http.StatusOK, result)
}

// GetJobErrors returns the error_log JSONB for a job as a parsed array.
func (h *Handler) GetJobErrors(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := h.jobRepo.GetByID(r.Context(), jobID)
	if err != nil {
		jsonError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	var errorLog []any
	if len(job.ErrorLog) > 0 {
		_ = json.Unmarshal(job.ErrorLog, &errorLog)
	}
	jsonOK(w, http.StatusOK, map[string]any{"job_id": jobID.String(), "errors": errorLog, "total": len(errorLog)})
}

// ListTables returns all supported source->target migration pairs.
func (h *Handler) ListTables(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, http.StatusOK, map[string]any{"supported": migration.SupportedTables()})
}

// Healthz returns 200 if the service is alive.
func (h *Handler) Healthz(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now().UTC()})
}

func parseJobID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := r.PathValue("job_id")
	id, err := uuid.Parse(raw)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_job_id", "job_id must be a valid UUID")
		return uuid.UUID{}, false
	}
	return id, true
}

func jsonOK(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message})
}
